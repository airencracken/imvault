// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"

	"imvault/internal/models"
)

func TestAlbumAccessRoundTrips(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	album, err := s.CreateAlbum(ctx, alice.ID, "Shared", "d",
		models.VisibilityMembers, models.AlbumAccessMembers)
	if err != nil {
		t.Fatalf("create album: %v", err)
	}
	if album.Access != models.AlbumAccessMembers {
		t.Errorf("access = %q right after creating, want members", album.Access)
	}

	reloaded, err := s.AlbumBySlug(ctx, album.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Access != models.AlbumAccessMembers {
		t.Errorf("access = %q after reloading, want members", reloaded.Access)
	}

	// Close it again, and the change sticks.
	if err := s.UpdateAlbum(ctx, album.ID, "Shared", "d",
		models.VisibilityMembers, models.AlbumAccessOwner); err != nil {
		t.Fatalf("update album: %v", err)
	}
	reloaded, err = s.AlbumBySlug(ctx, album.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Access != models.AlbumAccessOwner {
		t.Errorf("access = %q after closing, want owner", reloaded.Access)
	}

	// An unknown value does not widen anything.
	if _, err := s.CreateAlbum(ctx, alice.ID, "Odd", "", models.VisibilityMembers, models.AlbumAccess("anybody")); err != nil {
		t.Fatalf("create album: %v", err)
	}
	odd, err := s.AlbumBySlug(ctx, "odd")
	if err != nil {
		t.Fatal(err)
	}
	if odd.Access != models.AlbumAccessOwner {
		t.Errorf("access = %q for an unknown value, want owner", odd.Access)
	}
}

func TestAlbumsVisibleToListOthersAlbumsWithinTheirLevel(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")

	for _, album := range []struct {
		title      string
		visibility models.Visibility
	}{
		{"Public", models.VisibilityPublic},
		{"Members", models.VisibilityMembers},
		{"Private", models.VisibilityPrivate},
	} {
		if _, err := s.CreateAlbum(ctx, alice.ID, album.title, "",
			album.visibility, models.AlbumAccessMembers); err != nil {
			t.Fatalf("create %s: %v", album.title, err)
		}
	}

	// Bob sees what is shared with members, which is how a shared album is
	// found without being handed its link.
	visible, err := s.AlbumsVisibleTo(ctx, bob.ID)
	if err != nil {
		t.Fatalf("albums visible to: %v", err)
	}
	got := map[string]bool{}
	for _, album := range visible {
		got[album.Title] = true
	}
	if !got["Public"] || !got["Members"] {
		t.Errorf("a visible album is missing: %+v", got)
	}
	if got["Private"] {
		t.Error("somebody else's private album was listed")
	}

	// A caller's own albums belong to a different listing, so they must not be
	// here as well.
	if _, err := s.CreateAlbum(ctx, bob.ID, "Bobs", "", models.VisibilityPublic, models.AlbumAccessOwner); err != nil {
		t.Fatal(err)
	}
	visible, err = s.AlbumsVisibleTo(ctx, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, album := range visible {
		if album.Title == "Bobs" {
			t.Error("the caller's own album was listed as somebody else's")
		}
		if album.Username != "alice" {
			t.Errorf("album %q is attributed to %q, want alice", album.Title, album.Username)
		}
	}
}
