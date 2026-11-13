// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"

	"imvault/internal/models"
)

func TestSharingChoicesRoundTripAndDefaultHidden(t *testing.T) {
	s, ctx := newTestStore(t)
	u := mustUser(t, s, ctx, "alice")
	// A caller that sets nothing gets the cautious answer.
	f := mustFile(t, s, ctx, "zero", &u.ID, models.VisibilityMembers, nil)
	got, err := s.FileByID(ctx, f.ID)
	if err != nil || got.ShowCamera || got.ShowLocation {
		t.Fatalf("unset choices are not hidden: %+v %v", got, err)
	}
	for _, camera := range []bool{true, false} {
		for _, location := range []bool{true, false} {
			if err := s.UpdateFile(ctx, f.ID, FileUpdate{ShowCamera: &camera, ShowLocation: &location}); err != nil {
				t.Fatal(err)
			}
			got, err := s.FileByID(ctx, f.ID)
			if err != nil || got.ShowCamera != camera || got.ShowLocation != location {
				t.Fatalf("camera=%v location=%v stored as %+v %v", camera, location, got, err)
			}
		}
	}
}

func TestMakingAFilePublicHidesCameraDetailsAndLocation(t *testing.T) {
	s, ctx := newTestStore(t)
	u := mustUser(t, s, ctx, "alice")
	shown := true
	public, members := models.VisibilityPublic, models.VisibilityMembers

	setup := func(id string, visibility models.Visibility) *models.File {
		f := mustFile(t, s, ctx, id, &u.ID, visibility, nil)
		if err := s.UpdateFile(ctx, f.ID, FileUpdate{ShowCamera: &shown, ShowLocation: &shown}); err != nil {
			t.Fatal(err)
		}
		return f
	}
	check := func(id string, want bool) {
		t.Helper()
		got, err := s.FileByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.ShowLocation != want || got.ShowCamera != want {
			t.Fatalf("%s: location=%v camera=%v, want both %v", id, got.ShowLocation, got.ShowCamera, want)
		}
	}

	f := setup("single", models.VisibilityPrivate)
	if err := s.SetFileVisibility(ctx, f.ID, public); err != nil {
		t.Fatal(err)
	}
	check(f.ID, false)

	f = setup("update", models.VisibilityPrivate)
	if err := s.UpdateFile(ctx, f.ID, FileUpdate{Visibility: &public}); err != nil {
		t.Fatal(err)
	}
	check(f.ID, false)

	// An explicit choice in the same request wins.
	f = setup("explicit", models.VisibilityPrivate)
	if err := s.UpdateFile(ctx, f.ID, FileUpdate{Visibility: &public, ShowCamera: &shown, ShowLocation: &shown}); err != nil {
		t.Fatal(err)
	}
	check(f.ID, true)
	f = setup("mixed", models.VisibilityMembers)
	if err := s.UpdateFile(ctx, f.ID, FileUpdate{Visibility: &public, ShowCamera: &shown}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.FileByID(ctx, f.ID); err != nil || !got.ShowCamera || got.ShowLocation {
		t.Fatalf("explicit camera with implicit location: %+v %v", got, err)
	}

	// A file that is already public keeps what its owner chose.
	f = setup("already", models.VisibilityPublic)
	if err := s.SetFileVisibility(ctx, f.ID, public); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateFile(ctx, f.ID, FileUpdate{Visibility: &public}); err != nil {
		t.Fatal(err)
	}
	check(f.ID, true)

	// Closed levels never touch location.
	f = setup("closed", models.VisibilityPrivate)
	if err := s.SetFileVisibility(ctx, f.ID, members); err != nil {
		t.Fatal(err)
	}
	check(f.ID, true)

	a, b := setup("bulk-a", models.VisibilityMembers), setup("bulk-b", models.VisibilityPublic)
	if _, err := s.SetFilesVisibility(ctx, u.ID, []string{a.ID, b.ID}, public); err != nil {
		t.Fatal(err)
	}
	check(a.ID, false)
	check(b.ID, true)
}

func TestLocationUpdateAtomicityAndBlobKeys(t *testing.T) {
	s, ctx := newTestStore(t)
	u := mustUser(t, s, ctx, "alice")
	f := mustFile(t, s, ctx, "photo", &u.ID, models.VisibilityPrivate, nil)
	shown, hidden := true, false
	if err := s.UpdateFile(ctx, f.ID, FileUpdate{ShowCamera: &shown, ShowLocation: &hidden}); err != nil {
		t.Fatal(err)
	}
	bad := models.Visibility("invalid")
	if err := s.UpdateFile(ctx, f.ID, FileUpdate{Visibility: &bad, ShowCamera: &hidden, ShowLocation: &shown}); err == nil {
		t.Fatal("invalid visibility accepted")
	}
	f, err := s.FileByID(ctx, f.ID)
	if err != nil || !f.ShowCamera || f.ShowLocation {
		t.Fatal("invalid update partially committed")
	}
	for _, camera := range []bool{true, false} {
		if err := s.SetBlobFilteredKey(ctx, f.SHA256, filteredTestKey(camera), camera); err != nil {
			t.Fatal(err)
		}
	}
	blob, err := s.BlobBySHA(ctx, f.SHA256)
	if err != nil || len(blob.Keys()) != 5 {
		t.Fatalf("filtered keys omitted: %+v %v", blob, err)
	}
	if err := s.ClearBlobCleanKey(ctx, f.SHA256); err != nil {
		t.Fatal(err)
	}
	blob, err = s.BlobBySHA(ctx, f.SHA256)
	if err != nil || blob.CameraKey != "" || blob.LocationKey != "" {
		t.Fatal("cache invalidation missed filtered variants")
	}
}

func filteredTestKey(camera bool) string {
	if camera {
		return "camera.jpg"
	}
	return "location.jpg"
}
