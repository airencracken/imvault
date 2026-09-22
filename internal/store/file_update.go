// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"

	"imvault/internal/models"
)

// FileUpdate changes only supplied fields; a pointer to an empty description
// clears it. A single statement keeps combined API changes atomic.
type FileUpdate struct {
	Visibility  *models.Visibility
	Metadata    *models.MetadataPolicy
	Description *string
}

func (s *Store) UpdateFile(ctx context.Context, id string, update FileUpdate) error {
	if update.Visibility != nil && !update.Visibility.Valid() {
		return fmt.Errorf("invalid visibility %q", *update.Visibility)
	}
	if update.Metadata != nil && !update.Metadata.Valid() {
		return fmt.Errorf("invalid metadata policy %q", *update.Metadata)
	}
	if update.Description != nil {
		text, err := models.NormalizeFileDescription(*update.Description)
		if err != nil {
			return err
		}
		update.Description = &text
	}
	_, err := s.db.ExecContext(ctx, `UPDATE files SET
		visibility = COALESCE(?, visibility), metadata = COALESCE(?, metadata),
		description = COALESCE(?, description) WHERE id = ?`,
		update.Visibility, update.Metadata, update.Description, id)
	if err != nil {
		return fmt.Errorf("update file: %w", err)
	}
	return nil
}
