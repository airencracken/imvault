// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"

	"imvault/internal/models"
)

// FileUpdate changes only supplied fields; a pointer to an empty description
// clears it. A single statement keeps combined API changes atomic.
//
// Making a closed file public hides its camera details and location unless the
// same update says otherwise.
type FileUpdate struct {
	Visibility   *models.Visibility
	ShowCamera   *bool
	ShowLocation *bool
	Description  *string
}

func (s *Store) UpdateFile(ctx context.Context, id string, update FileUpdate) error {
	if update.Visibility != nil && !update.Visibility.Valid() {
		return fmt.Errorf("invalid visibility %q", *update.Visibility)
	}
	if update.Description != nil {
		text, err := models.NormalizeFileDescription(*update.Description)
		if err != nil {
			return err
		}
		update.Description = &text
	}
	var visibility any
	if update.Visibility != nil {
		visibility = string(*update.Visibility)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE files SET
		show_camera = CASE WHEN ? IS NOT NULL THEN ?
			WHEN ? = 'public' AND visibility <> 'public' THEN 0 ELSE show_camera END,
		show_location = CASE WHEN ? IS NOT NULL THEN ?
			WHEN ? = 'public' AND visibility <> 'public' THEN 0 ELSE show_location END,
		visibility = COALESCE(?, visibility),
		description = COALESCE(?, description) WHERE id = ?`,
		update.ShowCamera, update.ShowCamera, visibility,
		update.ShowLocation, update.ShowLocation, visibility,
		visibility, update.Description, id)
	if err != nil {
		return fmt.Errorf("update file: %w", err)
	}
	return nil
}
