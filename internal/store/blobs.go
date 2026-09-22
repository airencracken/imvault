// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"imvault/internal/models"
)

const blobColumns = `sha256, size, object_key, thumb_key, preview_key, clean_key, details_json, refcount, created_at`

func scanBlob(sc rowScanner) (*models.Blob, error) {
	var (
		b       models.Blob
		created int64
	)
	if err := sc.Scan(&b.SHA256, &b.Size, &b.ObjectKey, &b.ThumbKey,
		&b.PreviewKey, &b.CleanKey, &b.Details, &b.Refcount, &created); err != nil {
		return nil, err
	}
	b.CreatedAt = toTime(created)
	return &b, nil
}

// EnsureBlob records where a piece of content is stored, if it is not already
// known.
//
// The reference count is not touched: a trigger increments it when the file row
// that references the blob is inserted, so the only caller obligation is that
// the blob exists before the file does.
func (s *Store) EnsureBlob(ctx context.Context, sha string, size int64, objectKey, thumbKey, previewKey, details string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO blobs (sha256, size, object_key, thumb_key, preview_key, details_json, refcount, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?)
		ON CONFLICT (sha256) DO NOTHING`,
		sha, size, objectKey, thumbKey, previewKey, details, nowUnix())
	if err != nil {
		return fmt.Errorf("ensure blob: %w", err)
	}
	return nil
}

// SetBlobDetails replaces derived metadata without changing any stored bytes
// or file-level visibility settings. Identical uploads share these details.
func (s *Store) SetBlobDetails(ctx context.Context, sha, details string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE blobs SET details_json = ? WHERE sha256 = ?`, details, sha); err != nil {
		return fmt.Errorf("set blob details: %w", err)
	}
	return nil
}

// BlobsAfter pages through unique originals for metadata maintenance without
// keeping a database cursor open while their contents are read.
func (s *Store) BlobsAfter(ctx context.Context, sha string, limit int) ([]*models.Blob, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+blobColumns+` FROM blobs WHERE sha256 > ? ORDER BY sha256 LIMIT ?`, sha, limit)
	if err != nil {
		return nil, fmt.Errorf("list blobs: %w", err)
	}
	defer rows.Close()
	var blobs []*models.Blob
	for rows.Next() {
		blob, err := scanBlob(rows)
		if err != nil {
			return nil, err
		}
		blobs = append(blobs, blob)
	}
	return blobs, rows.Err()
}

// SetBlobCleanKey records where a metadata-free copy of some content lives.
//
// It only ever writes when the row has no clean key yet, so two requests racing
// to produce the same copy agree on whichever finished first and neither
// overwrites a key that already points at real bytes.
func (s *Store) SetBlobCleanKey(ctx context.Context, sha, key string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE blobs SET clean_key = ? WHERE sha256 = ? AND clean_key = ''`,
		key, sha); err != nil {
		return fmt.Errorf("set blob clean key: %w", err)
	}
	return nil
}

// ClearBlobCleanKey forgets a metadata-free copy, so the next request builds it
// again. It is how a copy that was never written, or was removed behind the
// server's back, repairs itself.
func (s *Store) ClearBlobCleanKey(ctx context.Context, sha string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE blobs SET clean_key = '' WHERE sha256 = ?`, sha); err != nil {
		return fmt.Errorf("clear blob clean key: %w", err)
	}
	return nil
}

// BlobBySHA returns the record for a piece of content.
func (s *Store) BlobBySHA(ctx context.Context, sha string) (*models.Blob, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+blobColumns+` FROM blobs WHERE sha256 = ?`, sha)
	b, err := scanBlob(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return b, nil
}

// DeleteBlobIfUnreferenced removes a blob that nothing points at any more, and
// returns it so the caller can remove the bytes.
//
// The condition is part of the statement rather than a separate read, so a blob
// that has just been referenced again is left alone.
func (s *Store) DeleteBlobIfUnreferenced(ctx context.Context, sha string) (*models.Blob, error) {
	row := s.db.QueryRowContext(ctx, `
		DELETE FROM blobs WHERE sha256 = ? AND refcount <= 0
		RETURNING `+blobColumns, sha)

	b, err := scanBlob(row)
	if err != nil {
		// Nothing was deleted, which is the ordinary case: somebody else still
		// has the content.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("delete blob: %w", err)
	}
	return b, nil
}

// OrphanedBlobs lists content that nothing refers to any more.
//
// It is the sweep that catches anything a deletion path missed, and it is why
// the count being wrong is recoverable rather than permanent.
func (s *Store) OrphanedBlobs(ctx context.Context, limit int) ([]*models.Blob, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+blobColumns+` FROM blobs WHERE refcount <= 0 ORDER BY created_at LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list orphaned blobs: %w", err)
	}
	defer rows.Close()

	var blobs []*models.Blob
	for rows.Next() {
		b, err := scanBlob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan blob: %w", err)
		}
		blobs = append(blobs, b)
	}
	return blobs, rows.Err()
}

// RecomputeBlobRefcounts recalculates every count from the file rows, and
// removes content nothing refers to.
//
// It exists for the same reason the storage recompute does: a count is derived
// state, and a way to put it back in step is worth having even when the
// database maintains it. It returns how many blobs were corrected.
func (s *Store) RecomputeBlobRefcounts(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE blobs SET refcount = (
			SELECT COUNT(*) FROM files f WHERE f.sha256 = blobs.sha256
		)
		WHERE refcount <> (
			SELECT COUNT(*) FROM files f WHERE f.sha256 = blobs.sha256
		)`)
	if err != nil {
		return 0, fmt.Errorf("recompute blob refcounts: %w", err)
	}
	return res.RowsAffected()
}

// BlobStats summarises content storage, for the admin dashboard.
type BlobStats struct {
	Blobs    int
	Files    int
	Shared   int
	Orphaned int
}

// BlobSummary reports how much de-duplication is saving.
func (s *Store) BlobSummary(ctx context.Context) (BlobStats, error) {
	var stats BlobStats

	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM blobs),
			(SELECT COUNT(*) FROM files),
			(SELECT COUNT(*) FROM blobs WHERE refcount > 1),
			(SELECT COUNT(*) FROM blobs WHERE refcount <= 0)`).
		Scan(&stats.Blobs, &stats.Files, &stats.Shared, &stats.Orphaned)
	if err != nil {
		return BlobStats{}, fmt.Errorf("blob summary: %w", err)
	}
	return stats, nil
}
