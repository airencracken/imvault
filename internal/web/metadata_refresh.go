// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"

	"imvault/internal/metadata"
	"imvault/internal/models"
)

// refreshFileDetails upgrades old cached metadata once per original. The same
// content lock used by uploads/deletion avoids duplicate work and lost updates.
// Storage failures remain retryable; unreadable metadata keeps known fields.
func (s *Server) refreshFileDetails(ctx context.Context, file *models.File) {
	if file.DetailsVersion >= metadata.Version {
		return
	}
	unlock, err := s.content.acquire(ctx, file.SHA256)
	if err != nil {
		return
	}
	defer unlock()
	blob, err := s.store.BlobBySHA(ctx, file.SHA256)
	if err != nil {
		return
	}
	file.Details, file.DetailsVersion = blob.Details, blob.DetailsVersion
	if blob.DetailsVersion >= metadata.Version {
		return
	}
	src, err := s.objects.Open(ctx, blob.ObjectKey)
	if err != nil {
		s.log.Warn("refresh photo details: open original", "id", file.ID, "error", err)
		return
	}
	defer src.Close()
	details, err := metadata.ExtractStored(src)
	if errors.Is(err, metadata.ErrRead) {
		s.log.Warn("refresh photo details: read original", "id", file.ID, "error", err)
		return
	}
	if err != nil {
		s.log.Debug("refresh photo details: no readable metadata", "id", file.ID, "error", err)
	}
	if ctx.Err() != nil {
		return
	}
	encoded := metadata.MergeDetails(blob.Details, details)
	if err := s.store.SetBlobDetails(ctx, blob.SHA256, encoded); err != nil {
		s.log.Error("refresh photo details: store", "id", file.ID, "error", err)
		return
	}
	file.Details, file.DetailsVersion = encoded, metadata.Version
}
