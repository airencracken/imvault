// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"

	"imvault/internal/imaging"
	"imvault/internal/media"
	"imvault/internal/models"
	"imvault/internal/storage"
	"imvault/internal/store"
)

func Regenerate(ctx context.Context, st *store.Store, objects storage.Backend, processor *media.Processor, videosOnly bool, out io.Writer) error {
	return eachBlob(ctx, st, func(blob *models.Blob) error {
		result, err := renderOriginal(ctx, objects, processor, blob, videosOnly)
		if err != nil {
			return err
		}
		if result == nil {
			return nil
		}
		if err := verify(ctx, objects, Object{blob.ObjectKey, blob.Size, blob.SHA256}); err != nil {
			return err
		}
		thumb, err := saveRendition(ctx, objects, "thumb", blob.SHA256, result.Thumb)
		if err != nil {
			return err
		}
		preview, err := saveRendition(ctx, objects, "preview", blob.SHA256, result.Preview)
		if err != nil {
			return err
		}
		// Publish both keys and measured clip details together. Old renditions
		// are retained so this repair does not also perform destructive cleanup.
		update := store.Renditions{Thumb: thumb, Preview: preview, Width: result.Width, Height: result.Height, DurationMS: result.DurationMS, FrameCount: result.FrameCount}
		if err := st.SetBlobRenditions(ctx, blob.SHA256, update); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Rebuilt %s\n", blob.ObjectKey)
		return err
	})
}

func renderOriginal(ctx context.Context, objects storage.Backend, processor *media.Processor, blob *models.Blob, videosOnly bool) (*media.Result, error) {
	src, err := objects.Open(ctx, blob.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	head := make([]byte, media.HeadSize)
	n, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	format := media.Classify(head[:n])
	if videosOnly && !format.IsVideo() {
		return nil, nil
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var result *media.Result
	if format.IsVideo() {
		if !processor.VideoEnabled() {
			return nil, errors.New("ffmpeg and ffprobe are required to regenerate video posters")
		}
		local, _ := objects.LocalPath(blob.ObjectKey)
		result, err = processor.ProcessVideo(ctx, src, local, format)
	} else {
		result, err = processor.ProcessStill(src, format)
	}
	if err != nil {
		return nil, err
	}
	if len(result.Warnings) != 0 {
		return nil, fmt.Errorf("rendition was not replaced: %s", result.Warnings[0])
	}
	return result, nil
}

func saveRendition(ctx context.Context, objects storage.Backend, kind, original string, rendition *imaging.Rendition) (string, error) {
	if rendition == nil {
		return "", nil
	}
	hash := sha256.Sum256(rendition.Data)
	checksum := hex.EncodeToString(hash[:])
	key := path.Join(kind, "rebuilt", original, checksum+"."+rendition.Ext)
	if _, err := objects.Save(ctx, key, bytes.NewReader(rendition.Data)); err != nil {
		return "", err
	}
	if err := verify(ctx, objects, Object{key, int64(len(rendition.Data)), checksum}); err != nil {
		return "", err
	}
	return key, nil
}
