// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

// seedFileWithOwner uploads one file as the given session and returns its id.
func uploadAs(t *testing.T, s *session, name, visibility string) string {
	t.Helper()

	resp, body := s.upload(map[string]string{"visibility": visibility}, []uploadFile{
		{name: name, data: pngFixture(t, 32, 32)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload %s = %d", name, resp.StatusCode)
	}
	return firstFileID(t, body)
}

func TestAModeratorMayRemoveButNotRepublish(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	bob := h.seedUser("bob")
	mod := h.seedUser("mod")
	if err := h.store.SetUserRole(t.Context(), mod.ID, models.RoleModerator); err != nil {
		t.Fatal(err)
	}

	// Bob uploads something private.
	bobSession := h.sessionFor(t, bob.ID)
	fileID := uploadAs(t, bobSession, "private.png", "private")

	modSession := h.sessionFor(t, mod.ID)

	// A moderator has to be able to look at what they are judging.
	if resp, _ := modSession.get("/f/" + fileID); resp.StatusCode != http.StatusOK {
		t.Errorf("a moderator cannot see a member's private file: %d", resp.StatusCode)
	}

	// Publishing somebody's private upload is not a moderation action, and a
	// moderator who could do it would be an escalation rather than a safeguard.
	resp, _ := modSession.post("/f/"+fileID+"/visibility", url.Values{"visibility": {"public"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a moderator republished a private file: %d, want 403", resp.StatusCode)
	}
	file, err := h.store.FileByID(t.Context(), fileID)
	if err != nil {
		t.Fatal(err)
	}
	if file.Visibility != models.VisibilityPrivate {
		t.Errorf("the file is now %q, want private", file.Visibility)
	}

	// Removing a bad file is the moderation action, and it works.
	resp, _ = modSession.post("/f/"+fileID+"/delete", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("a moderator could not remove a file: %d", resp.StatusCode)
	}
	if _, err := h.store.FileByID(t.Context(), fileID); err == nil {
		t.Error("the file survived the moderator's deletion")
	}
}

func TestAModeratorReachesContentButNotAccounts(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	mod := h.seedUser("mod")
	member := h.seedUser("member")
	if err := h.store.SetUserRole(t.Context(), mod.ID, models.RoleModerator); err != nil {
		t.Fatal(err)
	}

	modSession := h.sessionFor(t, mod.ID)
	memberSession := h.sessionFor(t, member.ID)

	// The content area is the moderator's.
	if resp, _ := modSession.get("/admin/files"); resp.StatusCode != http.StatusOK {
		t.Errorf("a moderator cannot reach the content area: %d", resp.StatusCode)
	}

	// Accounts, policy, and mail are not.
	for _, path := range []string{"/admin", "/admin/users", "/admin/settings", "/admin/mail"} {
		resp, _ := modSession.get(path)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("a moderator reached %s: %d, want 403", path, resp.StatusCode)
		}
	}

	// And a moderator cannot promote themselves or anybody else.
	resp, _ := modSession.post("/admin/users/"+itoa64(member.ID)+"/role",
		url.Values{"role": {"admin"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a moderator changed a role: %d, want 403", resp.StatusCode)
	}
	after, err := h.store.UserByID(t.Context(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Role != models.RoleMember {
		t.Errorf("the member's role is now %q", after.Role)
	}

	// An ordinary member reaches none of it.
	if resp, _ := memberSession.get("/admin/files"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a member reached the content area: %d, want 403", resp.StatusCode)
	}
}

func TestAnAdministratorGrantsARole(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	bob := h.seedUser("bob")

	resp, body := h.postHTMX("/admin/users/"+itoa64(bob.ID)+"/role",
		url.Values{"csrf_token": {h.csrf()}, "role": {"moderator"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set role = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "is now Moderator") {
		t.Errorf("the swap does not report the change: %s", truncate(body))
	}
	if !strings.Contains(body, `<option value="moderator" selected`) {
		t.Error("the role picker did not move to the new role")
	}

	after, err := h.store.UserByID(t.Context(), bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Role != models.RoleModerator {
		t.Errorf("stored role = %q, want moderator", after.Role)
	}

	// Demoting yourself is refused even when you are not the last one, because
	// it is nearly always a mistake.
	other := h.seedUser("other")
	if err := h.store.SetUserRole(t.Context(), other.ID, models.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	resp, body = h.postHTMX("/admin/users/"+itoa64(boss.ID)+"/role",
		url.Values{"csrf_token": {h.csrf()}, "role": {"member"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("self-demotion = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "cannot remove your own administrator rights") {
		t.Errorf("self-demotion was not refused: %s", truncate(body))
	}
	if after, err := h.store.UserByID(t.Context(), boss.ID); err != nil {
		t.Fatal(err)
	} else if !after.IsAdmin() {
		t.Error("the administrator demoted themselves")
	}
}

func TestAModeratorDoesNotGetAlbumAdministration(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	owner := h.seedUser("owner")
	mod := h.seedUser("mod")
	if err := h.store.SetUserRole(t.Context(), mod.ID, models.RoleModerator); err != nil {
		t.Fatal(err)
	}

	// The owner makes a private album.
	ownerSession := h.sessionFor(t, owner.ID)
	ownerSession.post("/albums", url.Values{
		"title":      {"Family"},
		"visibility": {"private"},
		"access":     {"owner"},
	})

	modSession := h.sessionFor(t, mod.ID)

	// A moderator can open it, to judge it.
	if resp, _ := modSession.get("/a/family"); resp.StatusCode != http.StatusOK {
		t.Errorf("a moderator cannot see a private album: %d", resp.StatusCode)
	}

	// But an album is not theirs to republish: making a private album public
	// would expose everything in it.
	resp, _ := modSession.post("/a/family/settings", url.Values{
		"title":      {"Family"},
		"visibility": {"public"},
		"access":     {"owner"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a moderator changed a private album's visibility: %d, want 403", resp.StatusCode)
	}
	album, err := h.store.AlbumBySlug(t.Context(), "family")
	if err != nil {
		t.Fatal(err)
	}
	if album.Visibility != models.VisibilityPrivate {
		t.Errorf("the album is now %q, want private", album.Visibility)
	}

	// Removing the album outright is a moderation action, and it works.
	resp, _ = modSession.post("/a/family/delete", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("a moderator could not remove an album: %d", resp.StatusCode)
	}
	if _, err := h.store.AlbumBySlug(t.Context(), "family"); err == nil {
		t.Error("the album survived the moderator's deletion")
	}
}

func TestAdminNavShowsAModeratorOnlyContent(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	mod := h.seedUser("mod")
	if err := h.store.SetUserRole(t.Context(), mod.ID, models.RoleModerator); err != nil {
		t.Fatal(err)
	}

	modSession := h.sessionFor(t, mod.ID)
	_, page := modSession.get("/admin/files")

	if !strings.Contains(page, `href="/admin/files"`) {
		t.Error("the moderator's navigation has no content link")
	}
	for _, forbidden := range []string{`href="/admin/users"`, `href="/admin/settings"`, `href="/admin/mail"`} {
		if strings.Contains(page, forbidden) {
			t.Errorf("the moderator's navigation offers %s", forbidden)
		}
	}
}
