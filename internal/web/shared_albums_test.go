// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

// sharedAlbum makes an album as the harness account and returns its slug.
func (h *harness) sharedAlbum(t *testing.T, title string, visibility models.Visibility, access models.AlbumAccess) {
	t.Helper()

	resp, _ := h.postForm("/albums", url.Values{
		"csrf_token": {h.csrf()},
		"title":      {title},
		"visibility": {string(visibility)},
		"access":     {string(access)},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create album %q = %d, want 303", title, resp.StatusCode)
	}
}

func TestASharedAlbumAcceptsAnybodysOwnFiles(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	bob := h.seedUser("bob")

	h.sharedAlbum(t, "Raid Night", models.VisibilityMembers, models.AlbumAccessMembers)

	// Bob uploads his own screenshot and adds it to Alice's album.
	bobSession := h.sessionFor(t, bob.ID)
	_, body := bobSession.upload(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "bobs.png", data: pngFixture(t, 32, 32)},
	})
	bobFile := firstFileID(t, body)

	resp, _ := bobSession.post("/a/raid-night/files", url.Values{"files": {bobFile}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("a contributor was refused: %d", resp.StatusCode)
	}

	// Alice sees it, and it is attributed to Bob.
	_, page := h.get("/a/raid-night")
	if !strings.Contains(page, bobFile) {
		t.Error("the owner cannot see the contribution")
	}
	if !strings.Contains(page, "bob") {
		t.Error("the album does not say who contributed")
	}

	// And Bob sees it too.
	_, bobsView := bobSession.get("/a/raid-night")
	if !strings.Contains(bobsView, bobFile) {
		t.Error("the contributor cannot see the album")
	}
}

func TestAContributorCannotChangeTheAlbum(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	bob := h.seedUser("bob")

	h.sharedAlbum(t, "Raid Night", models.VisibilityMembers, models.AlbumAccessMembers)
	bobSession := h.sessionFor(t, bob.ID)

	// Renaming is the owner's.
	resp, _ := bobSession.post("/a/raid-night/settings", url.Values{
		"title":      {"Hijacked"},
		"visibility": {"public"},
		"access":     {"owner"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a contributor renamed the album: %d, want 403", resp.StatusCode)
	}

	// So is deleting it.
	resp, _ = bobSession.post("/a/raid-night/delete", url.Values{})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a contributor deleted the album: %d, want 403", resp.StatusCode)
	}

	album, err := h.store.AlbumBySlug(t.Context(), "raid-night")
	if err != nil {
		t.Fatalf("the album is gone: %v", err)
	}
	if album.Title != "Raid Night" || album.Visibility != models.VisibilityMembers ||
		album.Access != models.AlbumAccessMembers {
		t.Errorf("the album changed under a contributor: %+v", album)
	}
}

func TestAContributorMayTakeBackOnlyTheirOwn(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	bob := h.seedUser("bob")

	h.sharedAlbum(t, "Raid Night", models.VisibilityMembers, models.AlbumAccessMembers)
	album, err := h.store.AlbumBySlug(t.Context(), "raid-night")
	if err != nil {
		t.Fatal(err)
	}

	// Bob's own file, added by Bob.
	bobSession := h.sessionFor(t, bob.ID)
	_, bobBody := bobSession.upload(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "bobs.png", data: pngFixture(t, 32, 32)},
	})
	bobFile := firstFileID(t, bobBody)
	if resp, _ := bobSession.post("/a/raid-night/files", url.Values{"files": {bobFile}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("add = %d", resp.StatusCode)
	}

	// Alice's file, added by Alice.
	_, aliceBody := h.uploadFiles(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "alices.png", data: pngFixture(t, 33, 33)},
	})
	aliceFile := firstFileID(t, aliceBody)
	if resp, _ := h.postForm("/a/raid-night/files", url.Values{"files": {aliceFile}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("owner add = %d", resp.StatusCode)
	}

	// Removing somebody else's is not a contributor's call.
	resp, _ := bobSession.post("/a/raid-night/files/"+aliceFile+"/delete", url.Values{})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a contributor removed somebody else's file: %d, want 403", resp.StatusCode)
	}
	members, err := h.store.AlbumMembership(t.Context(), album.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !members[aliceFile] {
		t.Error("the owner's file was removed anyway")
	}

	// Taking back their own is.
	resp, _ = bobSession.post("/a/raid-night/files/"+bobFile+"/delete", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("a contributor could not take back their own file: %d", resp.StatusCode)
	}
	members, err = h.store.AlbumMembership(t.Context(), album.ID)
	if err != nil {
		t.Fatal(err)
	}
	if members[bobFile] {
		t.Error("the contributor's own file is still there")
	}
}

func TestAnUnsharedAlbumRefusesOtherPeople(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	bob := h.seedUser("bob")

	// The default access is owner, and an existing album keeps meaning that.
	h.sharedAlbum(t, "Solo", models.VisibilityMembers, models.AlbumAccessOwner)

	bobSession := h.sessionFor(t, bob.ID)
	_, body := bobSession.upload(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "bobs.png", data: pngFixture(t, 32, 32)},
	})
	resp, _ := bobSession.post("/a/solo/files", url.Values{"files": {firstFileID(t, body)}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("an unshared album accepted somebody else's file: %d, want 403", resp.StatusCode)
	}

	// Bob can still look at it, because it is visible to members.
	if resp, _ := bobSession.get("/a/solo"); resp.StatusCode != http.StatusOK {
		t.Errorf("a member cannot see a members-visible album: %d", resp.StatusCode)
	}
}

func TestASharedAlbumCannotBePrivate(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")

	// A shared album only its owner can see is not shared with anybody, so it
	// is refused rather than stored and left to confuse the next person.
	resp, _ := h.postForm("/albums", url.Values{
		"csrf_token": {h.csrf()},
		"title":      {"Pointless"},
		"visibility": {"private"},
		"access":     {"members"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); !strings.Contains(location, "visible+to+members") &&
		!strings.Contains(location, "visible%20to%20members") {
		t.Errorf("the refusal did not explain itself: %q", location)
	}
	if _, err := h.store.AlbumBySlug(t.Context(), "pointless"); err == nil {
		t.Error("an incoherent album was created anyway")
	}
}

func TestSharedAlbumsAreDiscoverable(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	bob := h.seedUser("bob")

	h.sharedAlbum(t, "Raid Night", models.VisibilityMembers, models.AlbumAccessMembers)
	h.sharedAlbum(t, "Private Notes", models.VisibilityPrivate, models.AlbumAccessOwner)

	bobSession := h.sessionFor(t, bob.ID)
	_, page := bobSession.get("/albums")

	// The shared album is reachable without being handed a link.
	if !strings.Contains(page, "Raid Night") {
		t.Error("a shared album is not discoverable")
	}
	// Somebody else's private album is not.
	if strings.Contains(page, "Private Notes") {
		t.Error("somebody else's private album was listed")
	}
	// Nor is it listed as the viewer's own.
	if !strings.Contains(page, "Shared with everyone here") {
		t.Error("the shared section is missing")
	}
}

func TestAContributorIsWarnedAboutAPrivateFile(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	bob := h.seedUser("bob")

	h.sharedAlbum(t, "Raid Night", models.VisibilityMembers, models.AlbumAccessMembers)

	bobSession := h.sessionFor(t, bob.ID)
	_, body := bobSession.upload(map[string]string{"visibility": "private"}, []uploadFile{
		{name: "bobs.png", data: pngFixture(t, 32, 32)},
	})
	bobFile := firstFileID(t, body)

	resp, _ := bobSession.post("/a/raid-night/files", url.Values{"files": {bobFile}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("add = %d", resp.StatusCode)
	}

	// The file is in the album, but only Bob will see it there, and the notice
	// has to say so rather than let him assume otherwise.
	location, err := url.QueryUnescape(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(location, "only you will see it in this album") {
		t.Errorf("the contributor was not warned: %q", location)
	}
}

func TestAlbumAccessIsEditableByTheOwner(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	bob := h.seedUser("bob")

	// Start closed, then open it up.
	h.sharedAlbum(t, "Later Shared", models.VisibilityMembers, models.AlbumAccessOwner)

	bobSession := h.sessionFor(t, bob.ID)
	_, body := bobSession.upload(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "bobs.png", data: pngFixture(t, 32, 32)},
	})
	bobFile := firstFileID(t, body)
	if resp, _ := bobSession.post("/a/later-shared/files", url.Values{"files": {bobFile}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the album was open before it was shared: %d", resp.StatusCode)
	}

	resp, _ := h.postForm("/a/later-shared/settings", url.Values{
		"csrf_token": {h.csrf()},
		"title":      {"Later Shared"},
		"visibility": {"members"},
		"access":     {"members"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("settings = %d", resp.StatusCode)
	}

	album, err := h.store.AlbumBySlug(t.Context(), "later-shared")
	if err != nil {
		t.Fatal(err)
	}
	if album.Access != models.AlbumAccessMembers {
		t.Fatalf("access = %q after saving", album.Access)
	}

	// Now it accepts Bob's file.
	if resp, _ := bobSession.post("/a/later-shared/files", url.Values{"files": {bobFile}}); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("a shared album still refused a contributor: %d", resp.StatusCode)
	}
}

func TestAPIAlbumsSpeakAccess(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	bob := h.seedUser("bob")
	aliceKey := h.seedKey(alice.ID, "laptop", nil)
	bobKey := h.seedKey(bob.ID, "phone", nil)

	resp, raw := h.apiJSON(http.MethodPost, "/api/v1/albums", aliceKey, map[string]any{
		"title":      "Raid Night",
		"visibility": "members",
		"access":     "members",
	})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d: %s", resp.StatusCode, truncate(string(raw)))
	}
	album := decodeAlbum(t, raw)
	if album.Access != "members" {
		t.Fatalf("access = %q, want members", album.Access)
	}

	// Bob uploads and attaches through the API.
	_, uploadRaw := h.apiUpload(bobKey, map[string]string{"visibility": "members"}, map[string][]byte{
		"bobs.png": pngFixture(t, 32, 32),
	})
	uploaded := decodeUpload(t, uploadRaw)
	if len(uploaded.Files) != 1 {
		t.Fatalf("upload failed: %s", truncate(string(uploadRaw)))
	}

	resp, raw = h.apiJSON(http.MethodPost, "/api/v1/albums/"+album.Slug+"/files", bobKey,
		map[string]any{"files": []string{uploaded.Files[0].ID}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a contributor was refused by the API: %d: %s", resp.StatusCode, truncate(string(raw)))
	}
	var added struct {
		Added int `json:"added"`
	}
	if err := json.Unmarshal(raw, &added); err != nil {
		t.Fatal(err)
	}
	if added.Added != 1 {
		t.Errorf("added = %d, want 1", added.Added)
	}

	// Bob may take his own back through the API too.
	resp, _ = h.apiJSON(http.MethodDelete,
		"/api/v1/albums/"+album.Slug+"/files/"+uploaded.Files[0].ID, bobKey, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a contributor could not take their own file back: %d", resp.StatusCode)
	}

	// An invented access level is refused.
	resp, _ = h.apiJSON(http.MethodPost, "/api/v1/albums", aliceKey, map[string]any{
		"title":  "Nope",
		"access": "everybody",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an invented access level = %d, want 400", resp.StatusCode)
	}

	// And the contradictory combination is refused with an explanation.
	resp, _ = h.apiJSON(http.MethodPost, "/api/v1/albums", aliceKey, map[string]any{
		"title":      "Nope",
		"visibility": "private",
		"access":     "members",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a private shared album = %d, want 400", resp.StatusCode)
	}
}
