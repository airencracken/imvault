// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

// uploadInto uploads one file as a session, naming an album in the form.
func uploadInto(t *testing.T, s *session, albumID int64) string {
	t.Helper()
	_, body := s.upload(map[string]string{"album_id": itoa64(albumID), "visibility": "members"},
		[]uploadFile{{name: "x.png", data: pngFixture(t, 12, 12)}})
	return firstFileID(t, body)
}

// Uploading straight into an album follows the album page's rule: your own
// album, or a shared one you can see, and never anybody else's closed album.
func TestUploadingIntoAnAlbumFollowsTheAlbumRules(t *testing.T) {
	h := newHarness(t)
	alice, aliceKey := h.seedRole(t, "alice", models.RoleMember)
	bob, bobKey := h.seedRole(t, "bob", models.RoleMember)
	aliceS, bobS := h.sessionFor(t, alice.ID), h.sessionFor(t, bob.ID)

	for _, album := range []url.Values{
		{"title": {"Shared"}, "visibility": {"members"}, "access": {"members"}},
		{"title": {"Closed"}, "visibility": {"members"}},
	} {
		if resp, _ := aliceS.post("/albums", album); resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("create album = %d", resp.StatusCode)
		}
	}
	shared, err := h.store.AlbumBySlug(t.Context(), "shared")
	if err != nil {
		t.Fatal(err)
	}
	closed, err := h.store.AlbumBySlug(t.Context(), "closed")
	if err != nil {
		t.Fatal(err)
	}

	in := func(album *models.Album, id string) bool {
		t.Helper()
		members, err := h.store.AlbumMembership(t.Context(), album.ID)
		if err != nil {
			t.Fatal(err)
		}
		return members[id]
	}

	for _, tc := range []struct {
		name  string
		who   *session
		album *models.Album
		want  bool
	}{
		{"owner into own album", aliceS, closed, true},
		{"member into shared album", bobS, shared, true},
		{"member into closed album", bobS, closed, false},
	} {
		if got := in(tc.album, uploadInto(t, tc.who, tc.album.ID)); got != tc.want {
			t.Errorf("web %s: added = %v, want %v", tc.name, got, tc.want)
		}
	}

	for _, tc := range []struct {
		name, key string
		album     *models.Album
		want      bool
	}{
		{"owner into own album", aliceKey, closed, true},
		{"member into shared album", bobKey, shared, true},
		{"member into closed album", bobKey, closed, false},
	} {
		resp, raw := h.apiUpload(tc.key, map[string]string{"album": tc.album.Slug}, map[string][]byte{"y.png": pngFixture(t, 14, 14)})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("api upload = %d: %s", resp.StatusCode, raw)
		}
		id := decodeUpload(t, raw).Files[0].ID
		if got := in(tc.album, id); got != tc.want {
			t.Errorf("api %s: added = %v, want %v", tc.name, got, tc.want)
		}
	}

	// The uploader offers exactly the albums that would be accepted.
	_, page := bobS.get("/upload")
	if !strings.Contains(page, "Shared (shared by alice)") || strings.Contains(page, ">Closed<") {
		t.Error("the uploader's album menu does not match what it would accept")
	}
}
