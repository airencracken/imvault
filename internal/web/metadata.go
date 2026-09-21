// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"imvault/internal/metadata"
	"imvault/internal/models"
)

// cleanMarker distinguishes a metadata-free copy from the original it was made
// from, in the same content-addressed directory.
const cleanMarker = ".clean"

// cleanKeyFor is where a metadata-free copy of an object lives.
//
// The name is derived from the original's, so the copy inherits the sharding
// and the long-lived caching, and two requests cannot disagree about where it
// goes.
func cleanKeyFor(objectKey string) string {
	ext := path.Ext(objectKey)
	return strings.TrimSuffix(objectKey, ext) + cleanMarker + ext
}

// scopedObjectKey picks which stored object a request should be served from.
//
// The rule is the file's effective metadata policy: the most restrictive of its
// own setting, its visibility, and every album it is in. When that says hidden
// the original is not what goes out — a metadata-free copy is, built on demand
// so that a file made public later is covered without anything having to be
// reprocessed.
//
// A copy that cannot be built is reported rather than papered over. Serving the
// original would be the one failure that matters here: it would look exactly
// like success while shipping the coordinates anyway.
func (s *Server) scopedObjectKey(ctx context.Context, file *models.File, original string) (string, error) {
	policy, err := s.store.EffectiveMetadataPolicy(ctx, file)
	if err != nil {
		return "", err
	}
	if policy != models.MetadataHidden {
		return original, nil
	}
	return s.cleanObject(ctx, file)
}

// cleanObject returns the metadata-free copy of a file's content, building it
// if this is the first time anything has needed one.
//
// It hangs off the blob rather than the file because it is a function of the
// bytes: two files with the same content share a single copy, so the second one
// to be made public pays nothing.
func (s *Server) cleanObject(ctx context.Context, file *models.File) (string, error) {
	blob, err := s.store.BlobBySHA(ctx, file.SHA256)
	if err != nil {
		return "", fmt.Errorf("look up content: %w", err)
	}
	if blob.CleanKey != "" {
		return blob.CleanKey, nil
	}

	key := cleanKeyFor(blob.ObjectKey)
	if err := s.buildCleanObject(ctx, file, blob.ObjectKey, key); err != nil {
		return "", err
	}

	// Recorded after the bytes are written, so a failure leaves the row saying
	// nothing rather than pointing at a copy that is not there. The update only
	// writes while the column is empty, so a race resolves to one of two
	// identical keys.
	if err := s.store.SetBlobCleanKey(ctx, blob.SHA256, key); err != nil {
		return "", err
	}
	return key, nil
}

// buildCleanObject writes the metadata-free copy of one stored object.
func (s *Server) buildCleanObject(ctx context.Context, file *models.File, objectKey, cleanKey string) error {
	if file.IsVideo() {
		return s.buildCleanClip(ctx, objectKey, cleanKey)
	}

	src, err := s.objects.Open(objectKey)
	if err != nil {
		return fmt.Errorf("open the original: %w", err)
	}
	defer src.Close()

	// Bounded by the per-file upload limit, which is at most a few tens of
	// megabytes for a still image or an animation.
	data, err := io.ReadAll(src)
	if err != nil {
		return fmt.Errorf("read the original: %w", err)
	}

	if !metadata.Handled(data) {
		return fmt.Errorf("%s keeps its metadata inside the image, which cannot be removed without re-encoding",
			strings.ToUpper(strings.TrimPrefix(file.Ext, ".")))
	}

	cleaned, ok := metadata.Strip(data, file.Mime)
	if !ok {
		return fmt.Errorf("%s metadata could not be removed",
			strings.ToUpper(strings.TrimPrefix(file.Ext, ".")))
	}

	if _, err := s.objects.Save(cleanKey, bytes.NewReader(cleaned)); err != nil {
		return fmt.Errorf("store the clean copy: %w", err)
	}
	return nil
}

// buildCleanClip remuxes a clip without its container metadata.
//
// It needs ffmpeg and says so rather than falling back to the original. The
// caller surfaces that, so an operator can see which public clips still carry
// their metadata — the difference between an unfinished job and a silent one.
func (s *Server) buildCleanClip(ctx context.Context, objectKey, cleanKey string) error {
	if !s.media.VideoEnabled() {
		return errors.New("clips need ffmpeg to have their metadata removed, and it is not installed")
	}

	// ffmpeg needs a seekable file, and it picks its muxer from the extension,
	// so the temporary names keep the original's.
	ext := path.Ext(objectKey)

	srcPath, ok := s.objects.LocalPath(objectKey)
	if !ok {
		tmp, err := os.CreateTemp("", "imvault-scrub-src-*"+ext)
		if err != nil {
			return fmt.Errorf("temporary file: %w", err)
		}
		defer func() {
			tmp.Close()
			os.Remove(tmp.Name())
		}()

		src, err := s.objects.Open(objectKey)
		if err != nil {
			return fmt.Errorf("open the original: %w", err)
		}
		defer src.Close()

		if _, err := io.Copy(tmp, src); err != nil {
			return fmt.Errorf("copy the original: %w", err)
		}
		if err := tmp.Close(); err != nil {
			return fmt.Errorf("close the temporary file: %w", err)
		}
		srcPath = tmp.Name()
	}

	out, err := os.CreateTemp("", "imvault-scrub-dst-*"+ext)
	if err != nil {
		return fmt.Errorf("temporary file: %w", err)
	}
	outPath := out.Name()
	out.Close()
	defer os.Remove(outPath)

	if err := s.media.Video.Scrub(ctx, srcPath, outPath); err != nil {
		return err
	}

	cleaned, err := os.Open(outPath)
	if err != nil {
		return fmt.Errorf("read the scrubbed clip: %w", err)
	}
	defer cleaned.Close()

	if _, err := s.objects.Save(cleanKey, cleaned); err != nil {
		return fmt.Errorf("store the clean copy: %w", err)
	}
	return nil
}

// forgetCleanObject drops the record of a metadata-free copy that has gone
// missing, so the next request builds it again rather than failing forever.
func (s *Server) forgetCleanObject(ctx context.Context, sha string) {
	if err := s.store.ClearBlobCleanKey(ctx, sha); err != nil {
		s.log.Error("forget clean copy", "sha256", sha, "error", err)
	}
}

// MetadataGap describes why a file's metadata cannot be removed, or is empty
// when there is no gap.
//
// It exists so the interface can say when a public file is still carrying what
// it should not be. Silence there would be the worst outcome of all: the point
// of the feature is that people can tell.
func (s *Server) MetadataGap(file *models.File) string {
	if file.IsVideo() {
		if s.media.VideoEnabled() {
			return ""
		}
		return "This instance has no ffmpeg, so this clip still carries its own metadata."
	}
	if metadata.Cleans(file.Ext) {
		return ""
	}
	return "This image format keeps its metadata inside the image, where it cannot be removed without re-encoding."
}
