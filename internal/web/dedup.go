// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path"
	"time"

	"imvault/internal/ids"
	"imvault/internal/models"
	"imvault/internal/store"
)

// Uploads are stored under keys derived from their content, so the same bytes
// uploaded twice occupy the disk once. The hash was always recorded; this is
// what finally uses it.
//
// Renditions are keyed by content *and* by the settings that produced them, so
// changing a thumbnail bound does not make an existing file hand out a
// rendition at the old size: the new parameters simply derive a different key,
// and the old object becomes unreferenced and is collected on the next delete.

// keyScheme is bumped when the layout changes, so old and new objects cannot be
// confused for one another.
const keyScheme = "v1"

// hashSource reads a whole upload to derive its content hash and size.
//
// The source is read twice on the way to storage, once here and once to write
// it: the key cannot be known until the hash is, and the alternative is
// buffering the whole upload in memory.
func hashSource(src io.ReadSeeker) (string, int64, error) {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", 0, fmt.Errorf("rewind upload: %w", err)
	}

	hasher := sha256.New()
	size, err := io.Copy(hasher, src)
	if err != nil {
		return "", 0, fmt.Errorf("read upload: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// contentKeys derives the storage keys for an upload from its content hash.
//
// An empty thumbExt or previewExt means there is no rendition of that kind, as
// is the case for animations and clips, which serve their original instead.
func (s *Server) contentKeys(sha, ext, thumbExt, previewExt string) objectKeys {
	// Two leading characters of the hash as a directory, so a large instance
	// does not end up with one directory holding every object.
	shard := sha[:2]

	keys := objectKeys{
		object: path.Join("orig", keyScheme, shard, sha+"."+ext),
	}
	if thumbExt != "" {
		keys.thumb = path.Join("thumb", keyScheme, s.renditionTag(), shard, sha+"."+thumbExt)
	}
	if previewExt != "" {
		keys.preview = path.Join("preview", keyScheme, s.renditionTag(), shard, sha+"."+previewExt)
	}
	return keys
}

// renditionTag summarises the settings that produced a rendition.
//
// It goes in the object key, so a rendition made with different settings is a
// different object rather than a stale one served under the same name.
func (s *Server) renditionTag() string {
	summary := fmt.Sprintf("%d|%d|%d", s.cfg.ThumbMax, s.cfg.PreviewMax, s.cfg.JPEGQuality)
	sum := sha256.Sum256([]byte(summary))
	return hex.EncodeToString(sum[:])[:8]
}

// reusableUpload finds an identical upload whose stored objects this one can
// share, so the decode and the resize can be skipped entirely.
//
// A row stored by an older version has keys that do not match the current
// scheme, and is declined: reusing it would work, but it would not deduplicate.
func (s *Server) reusableUpload(ctx context.Context, sha string) (*models.File, bool) {
	existing, err := s.store.FileBySHA256(ctx, sha)
	if err != nil {
		return nil, false
	}

	thumbExt, previewExt := "", ""
	if existing.ThumbKey != "" {
		thumbExt = trimExt(existing.ThumbKey)
	}
	if existing.PreviewKey != "" {
		previewExt = trimExt(existing.PreviewKey)
	}

	want := s.contentKeys(sha, existing.Ext, thumbExt, previewExt)
	if existing.ObjectKey != want.object ||
		existing.ThumbKey != want.thumb ||
		existing.PreviewKey != want.preview {
		return nil, false
	}

	return existing, true
}

// trimExt returns a path's extension without the dot.
func trimExt(key string) string {
	ext := path.Ext(key)
	if ext == "" {
		return ""
	}
	return ext[1:]
}

// objectExists reports whether a stored object is already present.
//
// Used to skip rewriting identical bytes, which matters most for clips, where
// the second copy would be tens of megabytes of pointless I/O.
func (s *Server) objectExists(key string) bool {
	reader, err := s.objects.Open(key)
	if err != nil {
		return false
	}
	reader.Close()
	return true
}

// recordReused files an upload that shares its content with an existing one.
//
// Nothing is written: the row points at the objects already on disk. The
// storage quota is still claimed, because the account has one more file even
// though the server has no more bytes. Deduplication saves disk, not quota.
func (s *Server) recordReused(
	ctx context.Context,
	existing *models.File,
	header *multipart.FileHeader,
	owner *int64,
	options uploadOptions,
	expires *time.Time,
	now time.Time,
) (*models.File, error) {
	size := existing.Size

	if owner != nil {
		if err := s.store.ReserveStorage(ctx, *owner, size, s.policy().MaxTotalBytes); err != nil {
			return nil, quotaError(err, nil)
		}
	}

	for attempt := 0; attempt < 4; attempt++ {
		file := &models.File{
			ID:           ids.New(12),
			UserID:       owner,
			OriginalName: sanitiseFilename(header.Filename),
			Ext:          existing.Ext,
			Mime:         existing.Mime,
			Size:         size,
			Width:        existing.Width,
			Height:       existing.Height,
			SHA256:       existing.SHA256,
			ObjectKey:    existing.ObjectKey,
			ThumbKey:     existing.ThumbKey,
			PreviewKey:   existing.PreviewKey,
			Kind:         existing.Kind,
			FrameCount:   existing.FrameCount,
			DurationMS:   existing.DurationMS,
			Visibility:   options.Visibility,
			Metadata:     options.Metadata,
			CreatedAt:    now,
			ExpiresAt:    expires,
		}

		if err := s.store.CreateFile(ctx, file); err != nil {
			if errors.Is(err, store.ErrConflict) {
				continue // id collision: try again with a fresh id
			}
			s.releaseStorage(ctx, owner, size)
			return nil, fmt.Errorf("could not record the file")
		}
		return file, nil
	}

	s.releaseStorage(ctx, owner, size)
	return nil, fmt.Errorf("could not allocate a unique id")
}

// releaseBlob removes content once nothing refers to it any more.
//
// Identical uploads share their bytes, so deleting one file must not take the
// content out from under another. The reference count on the blob is what
// answers that, and the deletion is conditional on it, so a blob that has just
// been referenced again is left alone.
//
// Called after the file row is gone: the trigger on that delete has already
// brought the count down.
func (s *Server) releaseBlob(ctx context.Context, sha string) {
	if sha == "" {
		return
	}

	blob, err := s.store.DeleteBlobIfUnreferenced(ctx, sha)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("release blob", "sha256", sha, "error", err)
		}
		return
	}

	s.deleteKeys(blob.Keys())
}

// discardFailedUpload removes objects written for an upload that then failed,
// unless they belong to content somebody already has.
//
// That distinction matters: with content-addressed keys a repeat upload writes
// nothing, so the objects on disk may be an existing file's. Deleting them
// because this attempt failed would remove somebody else's image.
func (s *Server) discardFailedUpload(ctx context.Context, sha string, keys []string) {
	if blob, err := s.store.BlobBySHA(ctx, sha); err == nil {
		if !blob.Orphaned() {
			return // shared with an existing file; leave it alone
		}
		s.releaseBlob(ctx, sha)
		return
	}

	// No blob record, so nothing else can be referring to these.
	s.deleteKeys(keys)
}

// sweepOrphanedBlobs removes content that nothing refers to.
//
// It is the safety net for any path that removed file rows without releasing
// their content, and it is why a reference count that somehow fell out of step
// is recoverable rather than permanent.
func (s *Server) sweepOrphanedBlobs(ctx context.Context) {
	for round := 0; round < 20; round++ {
		orphans, err := s.store.OrphanedBlobs(ctx, 100)
		if err != nil {
			s.log.Error("sweep orphaned blobs", "error", err)
			return
		}
		if len(orphans) == 0 {
			return
		}

		for _, blob := range orphans {
			// Delete the row first: if that fails, the bytes are still
			// accounted for and can be retried, whereas removing the bytes
			// first would strand a row pointing at nothing.
			removed, err := s.store.DeleteBlobIfUnreferenced(ctx, blob.SHA256)
			if err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					s.log.Error("sweep: delete blob", "sha256", blob.SHA256, "error", err)
				}
				continue
			}
			s.deleteKeys(removed.Keys())
		}

		if len(orphans) < 100 {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// storeObject writes an object unless identical bytes are already there.
//
// With content-addressed keys a repeat write would be byte-for-byte the same
// file, and for a clip that is tens of megabytes of pointless I/O.
func (s *Server) storeObject(key string, src io.Reader) error {
	if s.objectExists(key) {
		return nil
	}
	if _, err := s.objects.Save(key, src); err != nil {
		return err
	}
	return nil
}
