// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Renditions are rebuilt derivatives of some content, with the measurements
// taken while rebuilding them.
type Renditions struct {
	Thumb, Preview            string
	Width, Height, FrameCount int
	DurationMS                int64
}

// SetBlobRenditions publishes rebuilt renditions and the measurements that came
// with them together, so no file is ever shown a new poster with stale
// dimensions.
func (s *Store) SetBlobRenditions(ctx context.Context, sha string, result Renditions) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE blobs SET thumb_key = ?, preview_key = ? WHERE sha256 = ?`, result.Thumb, result.Preview, sha); err != nil {
			return fmt.Errorf("set renditions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE files SET width = ?, height = ?, duration_ms = ?, frame_count = ? WHERE sha256 = ?`,
			result.Width, result.Height, result.DurationMS, result.FrameCount, sha); err != nil {
			return fmt.Errorf("set rendition measurements: %w", err)
		}
		return nil
	})
}
