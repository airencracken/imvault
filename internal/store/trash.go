// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"imvault/internal/models"
)

const TrashRetention = models.TrashRetention

// FileForModeration includes trashed uploads so moving a reported file to
// Trash cannot evade a moderator's permanent removal decision. Only staff
// routes may use this lookup; normal file and media routes use FileByID.
func (s *Store) FileForModeration(ctx context.Context, id string) (*models.File, error) {
	f, err := scanFile(s.db.QueryRowContext(ctx, `SELECT `+fileColumns+` `+fileFrom+` WHERE f.id = ?`, id))
	return f, mapErr(err)
}

// TrashFile is owner-only, including when the caller is an administrator.
// References, tags, album membership and quota are retained for restoration.
func (s *Store) TrashFile(ctx context.Context, id string, owner int64, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE files SET trashed_at = ? WHERE id = ? AND user_id = ? AND trashed_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, ts(at), id, owner, ts(at))
	return changedFile(res, err)
}

func changedFile(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RestoreTrashedFile(ctx context.Context, id string, owner int64, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE files SET trashed_at = NULL WHERE id = ? AND user_id = ? AND trashed_at > ? AND (expires_at IS NULL OR expires_at > ?)`, id, owner, at.Add(-TrashRetention).Unix(), ts(at))
	return changedFile(res, err)
}

func (s *Store) TrashedFile(ctx context.Context, id string, owner int64) (*models.File, error) {
	f, err := scanFile(s.db.QueryRowContext(ctx, `SELECT `+fileColumns+` `+fileFrom+` WHERE f.id = ? AND f.user_id = ? AND f.trashed_at IS NOT NULL`, id, owner))
	return f, mapErr(err)
}

func (s *Store) ListTrash(ctx context.Context, owner int64, limit, offset int) ([]*models.File, error) {
	if limit < 1 || limit > 200 {
		limit = 60
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileColumns+` `+fileFrom+` WHERE f.user_id = ? AND f.trashed_at IS NOT NULL ORDER BY f.trashed_at DESC, f.id DESC LIMIT ? OFFSET ?`, owner, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []*models.File{}
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func (s *Store) CountTrash(ctx context.Context, owner int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE user_id = ? AND trashed_at IS NOT NULL`, owner).Scan(&count)
	return count, err
}

// PurgeTrashedFile loads and deletes in the same transaction so a concurrent
// restoration cannot turn a stale purge request into deletion of a live file.
func (s *Store) PurgeTrashedFile(ctx context.Context, id string, owner int64) (*models.File, error) {
	var file *models.File
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		file, err = scanFile(tx.QueryRowContext(ctx, `SELECT `+fileColumns+` `+fileFrom+` WHERE f.id = ? AND f.user_id = ? AND f.trashed_at IS NOT NULL`, id, owner))
		if err != nil {
			return mapErr(err)
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return file, nil
}

// DeleteExpiredFile rechecks the deadline when deleting, including if another
// process has restored a file since the cleanup query ran.
func (s *Store) DeleteExpiredFile(ctx context.Context, id string, at time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ? AND ((expires_at IS NOT NULL AND expires_at <= ?) OR (trashed_at IS NOT NULL AND trashed_at <= ?))`, id, ts(at), at.Add(-TrashRetention).Unix())
	if err != nil {
		return false, fmt.Errorf("delete expired file: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
