// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
)

// RotateFile changes only this file's display correction. Relative turns are
// a single SQL update so concurrent clicks cannot overwrite one another.
func (s *Store) RotateFile(ctx context.Context, id, direction string) error {
	var expression string
	switch direction {
	case "left":
		expression = "(rotation + 270) % 360"
	case "right":
		expression = "(rotation + 90) % 360"
	case "reset":
		expression = "0"
	default:
		return fmt.Errorf("invalid rotation direction")
	}
	result, err := s.db.ExecContext(ctx, "UPDATE files SET rotation="+expression+" WHERE id=? AND kind='image'", id)
	if err != nil {
		return fmt.Errorf("rotate file: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
