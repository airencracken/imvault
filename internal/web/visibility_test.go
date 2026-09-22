// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"imvault/internal/config"
	"imvault/internal/models"
)

// setPolicy replaces the instance policy and reloads the cache, as the admin
// page does when it saves.
func (h *harness) setPolicy(t *testing.T, settings models.Settings) {
	t.Helper()

	if settings.AnonymousTTL <= 0 {
		settings.AnonymousTTL = time.Hour
	}
	if !settings.DefaultVisibility.Valid() {
		settings.DefaultVisibility = models.VisibilityMembers
	}
	if err := h.store.SaveSettings(t.Context(), settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	if err := h.srv.reloadSettings(t.Context()); err != nil {
		t.Fatalf("reload settings: %v", err)
	}
}

func TestMembersLevelIsVisibleToAccountsOnly(t *testing.T) {
	h := newHarness(t)
	alice := h.registerForm("alice")
	bob := h.seedUser("bob")

	aliceSession := h.sessionFor(t, alice.ID)
	resp, body := aliceSession.upload(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "members.png", data: pngFixture(t, 32, 32)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d, want 200", resp.StatusCode)
	}
	id := firstFileID(t, body)

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if file.Visibility != models.VisibilityMembers {
		t.Fatalf("stored at %q, want members", file.Visibility)
	}

	// Another account can open it, which is the whole point of the level.
	bobSession := h.sessionFor(t, bob.ID)
	if resp, _ := bobSession.get("/f/" + id); resp.StatusCode != http.StatusOK {
		t.Errorf("a member could not open a members-level file: %d", resp.StatusCode)
	}

	// A stranger cannot, and is not told it exists.
	anon := h.newSession(t)
	if resp, _ := anon.get("/f/" + id); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a stranger got %d for a members-level file, want 404", resp.StatusCode)
	}

	// Nor does it appear on the public landing page.
	if _, home := anon.get("/"); strings.Contains(home, id) {
		t.Error("a members-level file appeared on the public landing page")
	}
}

func TestPrivateLevelStaysWithItsOwner(t *testing.T) {
	h := newHarness(t)
	alice := h.registerForm("alice")
	bob := h.seedUser("bob")

	aliceSession := h.sessionFor(t, alice.ID)
	_, body := aliceSession.upload(map[string]string{"visibility": "private"}, []uploadFile{
		{name: "private.png", data: pngFixture(t, 32, 32)},
	})
	id := firstFileID(t, body)

	// Not even another account may see it.
	bobSession := h.sessionFor(t, bob.ID)
	if resp, _ := bobSession.get("/f/" + id); resp.StatusCode != http.StatusNotFound {
		t.Errorf("another account got %d for a private file, want 404", resp.StatusCode)
	}

	// The owner can, obviously.
	if resp, _ := aliceSession.get("/f/" + id); resp.StatusCode != http.StatusOK {
		t.Errorf("the owner got %d for their own private file", resp.StatusCode)
	}
}

func TestAnonymousUploadsAreAlwaysPublic(t *testing.T) {
	h := newHarness(t)

	// Even when the instance asks for something closed, and even when the form
	// asks for it: there is no account to scope to and no session to check, and
	// the link the uploader is handed would not open for them.
	h.setPolicy(t, models.Settings{
		AllowAnonymousUploads: true,
		DefaultVisibility:     models.VisibilityPrivate,
	})

	anon := h.newSession(t)
	resp, body := anon.upload(map[string]string{"visibility": "private"}, []uploadFile{
		{name: "anon.png", data: pngFixture(t, 32, 32)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous upload = %d", resp.StatusCode)
	}

	file, err := h.store.FileByID(t.Context(), firstFileID(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if file.Visibility != models.VisibilityPublic {
		t.Errorf("anonymous upload stored at %q, want public", file.Visibility)
	}
}

func TestDefaultVisibilityGovernsNewUploads(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	for _, level := range models.VisibilityLevels() {
		h.setPolicy(t, models.Settings{
			AllowAnonymousUploads: true,
			DefaultVisibility:     level,
		})

		// No visibility field at all: the instance default is the answer.
		resp, body := h.uploadFiles(map[string]string{}, []uploadFile{
			{name: "shot.png", data: pngFixture(t, 32, 32)},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("upload with default %s = %d", level, resp.StatusCode)
		}

		file, err := h.store.FileByID(t.Context(), firstFileID(t, body))
		if err != nil {
			t.Fatal(err)
		}
		if file.Visibility != level {
			t.Errorf("default %s produced a %s upload", level, file.Visibility)
		}
	}
}

func TestUploadFormOffersTheDefaultLevel(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	h.setPolicy(t, models.Settings{
		AllowAnonymousUploads: true,
		DefaultVisibility:     models.VisibilityPrivate,
	})

	_, page := h.get("/upload")

	for _, level := range models.VisibilityLevels() {
		if !strings.Contains(page, `value="`+string(level)+`"`) {
			t.Errorf("the upload form does not offer %s", level)
		}
	}
	// The instance default is the one preselected.
	if !strings.Contains(page, `value="private" checked`) {
		t.Error("the instance default is not preselected on the upload form")
	}
}

func TestVisibilityCanBeChangedAfterUpload(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	_, body := h.uploadFiles(map[string]string{"visibility": "private"}, []uploadFile{
		{name: "shot.png", data: pngFixture(t, 32, 32)},
	})
	id := firstFileID(t, body)

	resp, fragment := h.postHTMX("/f/"+id+"/visibility", map[string][]string{
		"csrf_token": {h.csrf()},
		"visibility": {"members"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("visibility change = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(fragment, "Members") {
		t.Error("the swapped control does not show the new level")
	}

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if file.Visibility != models.VisibilityMembers {
		t.Errorf("stored at %q after the change, want members", file.Visibility)
	}
}

func TestTheFeedShowsWhatEverybodyShared(t *testing.T) {
	h := newHarness(t)
	alice := h.registerForm("alice")
	bob := h.seedUser("bob")

	aliceSession := h.sessionFor(t, alice.ID)
	_, aliceBody := aliceSession.upload(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "alices.png", data: pngFixture(t, 32, 32)},
	})
	aliceFile := firstFileID(t, aliceBody)

	bobSession := h.sessionFor(t, bob.ID)
	_, bobBody := bobSession.upload(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "bobs.png", data: pngFixture(t, 33, 33)},
	})
	bobFile := firstFileID(t, bobBody)

	// The feed is what everybody shared, attributed.
	resp, feed := aliceSession.get("/recent")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feed = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(feed, aliceFile) {
		t.Error("the feed is missing alice's own upload")
	}
	if !strings.Contains(feed, bobFile) {
		t.Error("the feed is missing bob's upload")
	}
	if !strings.Contains(feed, "bob") {
		t.Error("the feed does not attribute bob's upload")
	}

	// The gallery is still just your own.
	_, gallery := aliceSession.get("/gallery")
	if !strings.Contains(gallery, aliceFile) {
		t.Error("the gallery is missing alice's own upload")
	}
	if strings.Contains(gallery, bobFile) {
		t.Error("the gallery showed somebody else's upload")
	}

	// And it is not public.
	anon := h.newSession(t)
	resp, _ = anon.get("/recent")
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("a stranger reached the feed: %d, want a redirect", resp.StatusCode)
	}
}

func TestSignedInLandingPageIsTheFeed(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	resp, _ := h.get("/")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("signed-in / = %d, want 303", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/recent" {
		t.Errorf("signed-in / went to %q, want /recent", got)
	}
}

func TestMembersLevelIsNotPubliclyCached(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	_, body := h.uploadFiles(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "shot.png", data: pngFixture(t, 32, 32)},
	})
	id := firstFileID(t, body)

	resp, _ := h.get("/f/" + id + "/raw")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("raw = %d", resp.StatusCode)
	}
	// A shared cache must not keep a members-only file for a stranger.
	if cache := resp.Header.Get("Cache-Control"); !strings.HasPrefix(cache, "private") {
		t.Errorf("Cache-Control = %q, want it to start with private", cache)
	}
}

func TestInstanceProfilesSetThePolicy(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.AllowSignup = true
		cfg.AllowAnonymousUploads = true
		cfg.DefaultVisibility = models.VisibilityMembers
		cfg.AnonymousTTL = 48 * time.Hour
	})
	h.provisionAdmin("boss")

	if _, ok := profileFor("nonsense"); ok {
		t.Error("an unknown profile was accepted")
	}

	resp, _ := h.postForm("/admin/settings", map[string][]string{
		"csrf_token": {h.csrf()},
		"profile":    {"personal"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("apply profile = %d, want 303", resp.StatusCode)
	}

	policy := h.srv.policy()
	if policy.AllowSignup || policy.AllowAnonymousUploads {
		t.Errorf("the personal profile left something open: %+v", policy)
	}
	if policy.DefaultVisibility != models.VisibilityPrivate {
		t.Errorf("default visibility = %q, want private", policy.DefaultVisibility)
	}
	// The retention window is deliberately untouched: changing it reaches
	// backwards over uploads already stored, so a preset must not do it by
	// accident.
	if policy.AnonymousTTL != 48*time.Hour {
		t.Errorf("retention = %s, want the configured 48h", policy.AnonymousTTL)
	}

	// The retention window in the form was not required for a profile.
	resp, _ = h.postForm("/admin/settings", map[string][]string{
		"csrf_token": {h.csrf()},
		"profile":    {"public"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("apply public profile = %d, want 303", resp.StatusCode)
	}
	policy = h.srv.policy()
	if !policy.AllowSignup || !policy.AllowAnonymousUploads {
		t.Errorf("the public profile did not open the instance: %+v", policy)
	}
	if policy.DefaultVisibility != models.VisibilityPublic {
		t.Errorf("default visibility = %q, want public", policy.DefaultVisibility)
	}
}

func TestAPISpeaksTheSameVisibilityLevels(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	// The upload path takes the same field the web form does.
	_, raw := h.apiUpload(key, map[string]string{"visibility": "members"}, map[string][]byte{
		"shot.png": pngFixture(t, 32, 32),
	})
	uploaded := decodeUpload(t, raw)
	if len(uploaded.Files) != 1 {
		t.Fatalf("upload failed: %s", truncate(string(raw)))
	}
	if uploaded.Files[0].Visibility != "members" {
		t.Errorf("uploaded at %q, want members", uploaded.Files[0].Visibility)
	}
	if uploaded.Files[0].Public {
		t.Error("a members-level upload reported itself as public")
	}

	// The level can be changed, and an invented one is refused rather than
	// quietly closing the file.
	var patched apiFileJSON
	resp, raw := h.apiJSON(http.MethodPatch, "/api/v1/files/"+uploaded.Files[0].ID, key,
		map[string]any{"visibility": "public"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch = %d: %s", resp.StatusCode, truncate(string(raw)))
	}
	if err := json.Unmarshal(raw, &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Visibility != "public" || !patched.Public {
		t.Errorf("after the patch: visibility=%q public=%v", patched.Visibility, patched.Public)
	}

	resp, _ = h.apiJSON(http.MethodPatch, "/api/v1/files/"+uploaded.Files[0].ID, key,
		map[string]any{"visibility": "sort-of-public"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a nonsense level = %d, want 400", resp.StatusCode)
	}

	// The older boolean still works, so existing clients keep going.
	resp, raw = h.apiJSON(http.MethodPatch, "/api/v1/files/"+uploaded.Files[0].ID, key,
		map[string]any{"public": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("boolean patch = %d: %s", resp.StatusCode, truncate(string(raw)))
	}
	if err := json.Unmarshal(raw, &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Visibility != "private" {
		t.Errorf("public=false gave %q, want private", patched.Visibility)
	}

	// Albums carry a level too.
	resp, raw = h.apiJSON(http.MethodPost, "/api/v1/albums", key,
		map[string]any{"title": "Levels", "visibility": "members"})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("album create = %d: %s", resp.StatusCode, truncate(string(raw)))
	}
	if album := decodeAlbum(t, raw); album.Visibility != "members" {
		t.Errorf("album visibility = %q, want members", album.Visibility)
	}
}
