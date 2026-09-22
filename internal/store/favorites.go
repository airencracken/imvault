// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
)

// SetFavorite saves or removes a private bookmark. Repeating either operation
// is harmless; adding an existing favorite preserves its original saved date.
// The caller must check that the account can currently view the file.
func (s *Store) SetFavorite(ctx context.Context, userID int64, fileID string, favorite bool) error {
	if !favorite {
		_, err := s.db.ExecContext(ctx, `DELETE FROM favorites WHERE user_id = ? AND file_id = ?`, userID, fileID)
		if err != nil {
			return fmt.Errorf("remove favorite: %w", err)
		}
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO favorites (user_id, file_id, created_at) VALUES (?, ?, ?)
		ON CONFLICT (user_id, file_id) DO NOTHING`, userID, fileID, nowUnix())
	if err != nil {
		return fmt.Errorf("save favorite: %w", err)
	}
	return nil
}

func (s *Store) IsFavorite(ctx context.Context, userID int64, fileID string) (bool, error) {
	var favorite bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM favorites WHERE user_id = ? AND file_id = ?)`, userID, fileID).Scan(&favorite)
	if err != nil {
		return false, fmt.Errorf("check favorite: %w", err)
	}
	return favorite, nil
}
