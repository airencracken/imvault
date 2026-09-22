// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"sync"
	"testing"
	"time"

	"imvault/internal/models"
)

func TestFavoritesArePrivateIdempotentAndCascade(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")
	file := mustFile(t, s, ctx, "photo", &alice.ID, models.VisibilityPublic, nil)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Go(func() {
			if err := s.SetFavorite(ctx, alice.ID, file.ID, true); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	count, err := s.CountFiles(ctx, FileQuery{FavoritedBy: &alice.ID})
	if err != nil || count != 1 {
		t.Fatalf("concurrent saves: count=%d err=%v", count, err)
	}
	if yes, err := s.IsFavorite(ctx, bob.ID, file.ID); err != nil || yes {
		t.Fatal("favorite leaked to another account")
	}
	if err := s.SetFavorite(ctx, bob.ID, file.ID, false); err != nil {
		t.Fatal(err)
	}
	if yes, err := s.IsFavorite(ctx, alice.ID, file.ID); err != nil || !yes {
		t.Fatal("another account removed the owner's favorite")
	}
	if err := s.SetFavorite(ctx, bob.ID, file.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, bob.ID); err != nil {
		t.Fatal(err)
	}
	if yes, err := s.IsFavorite(ctx, bob.ID, file.ID); err != nil || yes {
		t.Fatal("deleted account left a favorite behind")
	}
	if err := s.DeleteFile(ctx, file.ID); err != nil {
		t.Fatal(err)
	}
	if yes, err := s.IsFavorite(ctx, alice.ID, file.ID); err != nil || yes {
		t.Fatal("deleted file left a favorite behind")
	}
	if err := s.SetFavorite(ctx, alice.ID, "missing", true); err == nil {
		t.Fatal("favorite accepted a nonexistent file")
	}
}

func TestFavoriteListingsRespectVisibilityExpirySearchAndPagination(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")
	expired := time.Now().Add(-time.Hour)
	files := []*models.File{
		mustFile(t, s, ctx, "public-photo", &bob.ID, models.VisibilityPublic, nil),
		mustFile(t, s, ctx, "members-photo", &bob.ID, models.VisibilityMembers, nil),
		mustFile(t, s, ctx, "my-private-photo", &alice.ID, models.VisibilityPrivate, nil),
		mustFile(t, s, ctx, "hidden-photo", &bob.ID, models.VisibilityPrivate, nil),
		mustFile(t, s, ctx, "expired-photo", nil, models.VisibilityPublic, &expired),
	}
	for i, file := range files {
		if err := s.SetFavorite(ctx, alice.ID, file.ID, true); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE favorites SET created_at = ? WHERE user_id = ? AND file_id = ?`, i+1, alice.ID, file.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Re-saving an older favorite must not move it ahead of newer favorites.
	if err := s.SetFavorite(ctx, alice.ID, files[0].ID, true); err != nil {
		t.Fatal(err)
	}
	query := FileQuery{FavoritedBy: &alice.ID, VisibleTo: &alice.ID, Limit: 1, Offset: 1}
	count, err := s.CountFiles(ctx, query)
	if err != nil || count != 3 {
		t.Fatalf("visible count=%d err=%v", count, err)
	}
	listed, err := s.ListFiles(ctx, query)
	if err != nil || len(listed) != 1 || listed[0].ID != "members-photo" {
		t.Fatalf("saved-order pagination: files=%v err=%v", listed, err)
	}
	query.Offset = 0
	query.Search = "public-photo"
	listed, err = s.ListFiles(ctx, query)
	if err != nil || len(listed) != 1 || listed[0].ID != "public-photo" {
		t.Fatalf("search: files=%v err=%v", listed, err)
	}
	if err := s.SetFileVisibility(ctx, "public-photo", models.VisibilityPrivate); err != nil {
		t.Fatal(err)
	}
	count, err = s.CountFiles(ctx, query)
	if err != nil || count != 0 {
		t.Fatal("favorite kept a file visible after its owner made it private")
	}
	for i := 0; i < 2; i++ {
		if err := s.SetFavorite(ctx, alice.ID, "public-photo", false); err != nil {
			t.Fatal(err)
		}
	}
	if yes, err := s.IsFavorite(ctx, alice.ID, "public-photo"); err != nil || yes {
		t.Fatal("favorite was not removed")
	}
}
