// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
)

type Renditions struct {
	Thumb, Preview            string
	Width, Height, FrameCount int
	DurationMS                int64
}

func (s *Store) SetBlobRenditions(ctx context.Context, sha string, result Renditions) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE blobs SET thumb_key = ?, preview_key = ? WHERE sha256 = ?`, result.Thumb, result.Preview, sha); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE files SET width = ?, height = ?, duration_ms = ?, frame_count = ? WHERE sha256 = ?`,
			result.Width, result.Height, result.DurationMS, result.FrameCount, sha)
		return err
	})
}
