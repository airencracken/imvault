// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"fmt"

	"imvault/internal/models"
)

// withOwnedFiles checks the entire selection before writing, within the same
// transaction. A mixed, missing, expired or trashed selection changes nothing.
func (s *Store) withOwnedFiles(ctx context.Context, owner int64, ids []string, change func(*sql.Tx, []string) error) (int, error) {
	files, err := bulkTagValues(ids, MaxBulkTagFiles, 128, "files")
	if err != nil {
		return 0, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		for _, id := range files {
			var found int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM files f WHERE f.id = ? AND f.user_id = ? AND `+expiryClause, id, owner, nowUnix()).Scan(&found); err != nil {
				return mapErr(err)
			}
		}
		return change(tx, files)
	})
	if err != nil {
		return 0, err
	}
	return len(files), nil
}

func (s *Store) SetFilesVisibility(ctx context.Context, owner int64, ids []string, visibility models.Visibility) (int, error) {
	if !visibility.Valid() {
		return 0, fmt.Errorf("%w: choose public, members or private", ErrInvalidTags)
	}
	return s.withOwnedFiles(ctx, owner, ids, func(tx *sql.Tx, files []string) error {
		for _, id := range files {
			if _, err := tx.ExecContext(ctx, `UPDATE files SET visibility = ? WHERE id = ?`, string(visibility), id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) AddFilesToAlbum(ctx context.Context, owner int64, ids []string, albumID int64) (int, error) {
	return s.withOwnedFiles(ctx, owner, ids, func(tx *sql.Tx, files []string) error {
		var allowed int
		// Recheck the album inside this transaction, including changes to its
		// contribution policy since the gallery was opened.
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM albums WHERE id = ? AND (user_id = ? OR (access = 'members' AND visibility IN ('public','members')) OR EXISTS (SELECT 1 FROM users WHERE id = ? AND role = 'admin'))`, albumID, owner, owner).Scan(&allowed)
		if err != nil {
			return mapErr(err)
		}
		return addFilesToAlbumTx(ctx, tx, albumID, files)
	})
}

// CreateAlbumWithFiles creates a private, owner-managed album and adds the
// entire owned selection. Failed validation or membership writes leave no album.
func (s *Store) CreateAlbumWithFiles(ctx context.Context, owner int64, ids []string, title string) (int, error) {
	titles, err := bulkTagValues([]string{title}, 1, 120, "album title")
	if err != nil {
		return 0, err
	}
	return s.withOwnedFiles(ctx, owner, ids, func(tx *sql.Tx, files []string) error {
		album, err := createAlbum(ctx, tx, owner, AlbumInput{Title: titles[0]})
		if err != nil {
			return err
		}
		return addFilesToAlbumTx(ctx, tx, album.ID, files)
	})
}

func addFilesToAlbumTx(ctx context.Context, tx *sql.Tx, albumID int64, files []string) error {
	for _, id := range files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO album_files (album_id, file_id, added_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, albumID, id, nowUnix()); err != nil {
			return err
		}
	}
	return nil
}
