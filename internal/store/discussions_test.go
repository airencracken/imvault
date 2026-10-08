// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"imvault/internal/models"
	"testing"
)

func TestAlbumDiscussionOwnershipAndIdempotence(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "owner")
	other := mustUser(t, s, ctx, "other")
	album, err := s.CreateAlbum(ctx, owner.ID, AlbumInput{Title: "Album", Visibility: models.VisibilityPrivate})
	if err != nil {
		t.Fatal(err)
	}
	raw := "https://board.example/topics/1"
	if err = s.LinkAlbumDiscussion(ctx, other.ID, album.ID, raw, false); !errors.Is(err, ErrNotFound) {
		t.Fatal("nonowner", err)
	}
	for i := 0; i < 2; i++ {
		if err = s.LinkAlbumDiscussion(ctx, owner.ID, album.ID, raw, false); err != nil {
			t.Fatal(err)
		}
	}
	links, err := s.AlbumDiscussions(ctx, album.ID)
	if err != nil || len(links) != 1 {
		t.Fatal(links, err)
	}
	if err = s.LinkAlbumDiscussion(ctx, owner.ID, album.ID, "javascript:alert(1)", false); err == nil {
		t.Fatal("unsafe URL")
	}
	if err = s.LinkAlbumDiscussion(ctx, owner.ID, album.ID, raw, true); err != nil {
		t.Fatal(err)
	}
	links, _ = s.AlbumDiscussions(ctx, album.ID)
	if len(links) != 0 {
		t.Fatal(links)
	}
}

func TestAlbumPreviewSnapshotNeverPairsPrivateTitleWithPublicAccess(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "owner")
	album, err := s.CreateAlbum(ctx, owner.ID, AlbumInput{Title: "Public", Visibility: models.VisibilityPublic})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			title := "SECRET"
			visibility := models.VisibilityPrivate
			if i%2 == 1 {
				title = "Public"
				visibility = models.VisibilityPublic
			}
			if err := s.UpdateAlbum(ctx, album.ID, AlbumInput{Title: title, Visibility: visibility}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 100; i++ {
		summary, err := s.PublicAlbumPreview(ctx, owner.ID, album.ID, false)
		if err == nil && summary.Title != "Public" {
			t.Fatal("private title exposed", summary)
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
