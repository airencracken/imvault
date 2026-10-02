// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"

	"imvault/internal/metadata"
	"imvault/internal/models"
	"imvault/internal/store"
)

// seedRole creates an account with a role and returns an API key for it.
func (h *harness) seedRole(t *testing.T, username string, role models.Role) (*models.User, string) {
	t.Helper()
	user, err := h.store.CreateUser(t.Context(), store.NewUser{
		Username: username, Email: username + "@example.com", PasswordHash: "hash", Role: role,
	})
	if err != nil {
		t.Fatal(err)
	}
	return user, h.seedKey(user.ID, "matrix", nil)
}

// gpsPhoto is a real camera JPEG carrying a location and a camera model.
func gpsPhoto(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../metadata/testdata/gps-values-before-directory.jpg")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// contribute uploads one located photo as the session's account and adds it
// to an album.
func contribute(t *testing.T, s *session, slug, name, visibility string) string {
	t.Helper()
	_, body := s.upload(map[string]string{
		"visibility": visibility,
		"metadata":   "hidden",
		"location":   "hidden",
	}, []uploadFile{{name: name, data: gpsPhoto(t)}})
	id := firstFileID(t, body)
	if resp, _ := s.post("/a/"+slug+"/files", url.Values{"files": {id}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("add %s to %s = %d", name, slug, resp.StatusCode)
	}
	return id
}

// TestAPIAlbumListingAuthorizationMatrix is the table for GET
// /api/v1/albums/{ref}: who may read a shared album, and which of a
// contributor's files each of them is told about.
func TestAPIAlbumListingAuthorizationMatrix(t *testing.T) {
	h := newHarness(t)
	owner, ownerKey := h.seedRole(t, "alice", models.RoleMember)
	contributor, contributorKey := h.seedRole(t, "bob", models.RoleMember)
	_, memberKey := h.seedRole(t, "carol", models.RoleMember)
	_, moderatorKey := h.seedRole(t, "mo", models.RoleModerator)
	_, adminKey := h.seedRole(t, "boss", models.RoleAdmin)

	ownerSession := h.sessionFor(t, owner.ID)
	if resp, _ := ownerSession.post("/albums", url.Values{
		"title": {"Raid Night"}, "visibility": {"members"}, "access": {"members"},
	}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create album = %d", resp.StatusCode)
	}
	ownPrivate := contribute(t, ownerSession, "raid-night", "own-private.jpg", "private")

	bobSession := h.sessionFor(t, contributor.ID)
	files := map[string]string{
		"private": contribute(t, bobSession, "raid-night", "bob-private.jpg", "private"),
		"members": contribute(t, bobSession, "raid-night", "bob-members.jpg", "members"),
		"public":  contribute(t, bobSession, "raid-night", "bob-public.jpg", "public"),
	}

	cases := []struct {
		name    string
		key     string
		status  int
		visible map[string]bool
		// seesDetails is whether hidden metadata is shown, which only the
		// people who may change a file are.
		seesDetails bool
	}{
		{"album owner", ownerKey, http.StatusOK, map[string]bool{"private": false, "members": true, "public": true}, false},
		{"administrator", adminKey, http.StatusOK, map[string]bool{"private": false, "members": true, "public": true}, true},
		{"contributor", contributorKey, http.StatusNotFound, nil, false},
		{"other member", memberKey, http.StatusNotFound, nil, false},
		{"moderator", moderatorKey, http.StatusNotFound, nil, false},
		{"no key", "", http.StatusUnauthorized, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, raw := h.apiDo(http.MethodGet, "/api/v1/albums/raid-night", tc.key, nil, "")
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.status, raw)
			}
			if tc.visible == nil {
				return
			}
			var album struct {
				Files []apiFileJSON `json:"files"`
			}
			if err := json.Unmarshal(raw, &album); err != nil {
				t.Fatal(err)
			}
			listed := map[string]apiFileJSON{}
			for _, f := range album.Files {
				listed[f.ID] = f
			}
			for level, want := range tc.visible {
				entry, got := listed[files[level]]
				if got != want {
					t.Errorf("bob's %s file listed = %v, want %v", level, got, want)
				}
				located := got && entry.Details != nil && entry.Details.Latitude != nil
				if got && located != tc.seesDetails {
					t.Errorf("bob's %s file location shown = %v, want %v", level, located, tc.seesDetails)
				}
			}
			if tc.name == "album owner" {
				own, ok := listed[ownPrivate]
				if !ok {
					t.Error("the owner's own private file is missing from their listing")
				} else if own.Details == nil || own.Details.Latitude == nil {
					t.Error("the owner is not shown their own file's location")
				}
			}
		})
	}
}

func TestWithholdDetails(t *testing.T) {
	lat, lon := 51.5, -0.1
	full := &detailsFixture{Camera: "X100", Artist: "Bob", Latitude: &lat, Longitude: &lon}
	for _, tc := range []struct {
		exif, location     bool
		camera, coordinate bool
	}{
		{true, true, true, true},
		{true, false, true, false},
		{false, true, false, true},
		{false, false, false, false},
	} {
		t.Run(fmt.Sprintf("exif=%v,location=%v", tc.exif, tc.location), func(t *testing.T) {
			got := withholdDetails(full.details(), tc.exif, tc.location)
			camera := got != nil && got.Camera != ""
			coordinate := got != nil && got.Latitude != nil
			if camera != tc.camera || coordinate != tc.coordinate {
				t.Errorf("camera=%v coordinate=%v, want %v %v", camera, coordinate, tc.camera, tc.coordinate)
			}
		})
	}
	if withholdDetails(nil, false, false) != nil {
		t.Error("nil details grew contents")
	}
	original := full.details()
	withholdDetails(original, false, false)
	if original.Latitude == nil || original.Camera == "" {
		t.Error("withholding edited the caller's details in place")
	}
}

type detailsFixture struct {
	Camera, Artist      string
	Latitude, Longitude *float64
}

func (f *detailsFixture) details() *metadata.Details {
	return &metadata.Details{Camera: f.Camera, Artist: f.Artist, Latitude: f.Latitude, Longitude: f.Longitude}
}
