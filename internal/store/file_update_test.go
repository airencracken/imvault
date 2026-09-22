// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"

	"imvault/internal/models"
)

func TestFileUpdateFailureDoesNotPartiallyChangeVisibility(t *testing.T) {
	s, ctx := newTestStore(t)
	user := mustUser(t, s, ctx, "alice")
	file := mustFile(t, s, ctx, "photo", &user.ID, models.VisibilityPrivate, nil)
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_description
		BEFORE UPDATE OF description ON files
		WHEN NEW.description = 'rejected'
		BEGIN SELECT RAISE(ABORT, 'simulated write failure'); END`); err != nil {
		t.Fatal(err)
	}
	visibility, metadata, description := models.VisibilityPublic, models.MetadataHidden, "rejected"
	if err := s.UpdateFile(ctx, file.ID, FileUpdate{
		Visibility: &visibility, Metadata: &metadata, Description: &description,
	}); err == nil {
		t.Fatal("write failure was not reported")
	}
	got, err := s.FileByID(ctx, file.ID)
	if err != nil || got.Visibility != file.Visibility || got.Metadata != file.Metadata || got.Description != "" {
		t.Fatalf("failed update changed the file: %#v (%v)", got, err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER reject_description`); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateFile(ctx, file.ID, FileUpdate{Description: &description}); err != nil {
		t.Fatal(err)
	}
	got, err = s.FileByID(ctx, file.ID)
	if err != nil || got.Description != description || got.Visibility != file.Visibility || got.Metadata != file.Metadata {
		t.Fatalf("description edit changed unrelated fields: %#v (%v)", got, err)
	}
}
