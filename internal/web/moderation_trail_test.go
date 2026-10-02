// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

// moderationActions lists the trail's actions, oldest first.
func moderationActions(t *testing.T, h *harness) []models.ModerationAction {
	t.Helper()
	entries, _, err := h.store.ListModerationLog(t.Context(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]models.ModerationAction, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		out = append(out, entries[i].Action)
	}
	return out
}

// An administrator removing somebody else's content through the API leaves the
// same record as doing it through the pages; the owner tidying up does not.
func TestAPIRemovalsReachTheModerationLog(t *testing.T) {
	h := newHarness(t)
	alice, aliceKey := h.seedRole(t, "alice", models.RoleMember)
	_, adminKey := h.seedRole(t, "boss", models.RoleAdmin)
	aliceS := h.sessionFor(t, alice.ID)

	own := uploadAs(t, aliceS, "own.png", "members")
	if resp, _ := h.apiDo(http.MethodDelete, "/api/v1/files/"+own, aliceKey, nil, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("owner delete = %d", resp.StatusCode)
	}
	if got := moderationActions(t, h); len(got) != 0 {
		t.Fatalf("an owner deleting their own file was logged: %v", got)
	}

	theirs := uploadAs(t, aliceS, "theirs.png", "members")
	if resp, _ := h.apiDo(http.MethodDelete, "/api/v1/files/"+theirs, adminKey, nil, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("admin file delete = %d", resp.StatusCode)
	}
	if resp, _ := aliceS.post("/albums", url.Values{"title": {"Trip"}, "visibility": {"members"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create album = %d", resp.StatusCode)
	}
	if resp, _ := h.apiDo(http.MethodDelete, "/api/v1/albums/trip", adminKey, nil, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("admin album delete = %d", resp.StatusCode)
	}

	got := moderationActions(t, h)
	want := []models.ModerationAction{models.ActionRemoveFile, models.ActionRemoveAlbum}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("trail = %v, want %v", got, want)
	}
}

func TestAccountDeletionByAnAdministratorIsLogged(t *testing.T) {
	h := newHarness(t)
	admin := h.provisionAdmin("boss")
	victim := h.seedUser("alice")
	s := h.sessionFor(t, admin.ID)
	if resp, _ := s.post("/admin/users/"+itoa64(victim.ID)+"/delete", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("admin delete = %d", resp.StatusCode)
	}
	entries, _, err := h.store.ListModerationLog(t.Context(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Action != models.ActionRemoveAccount || entries[0].TargetLabel != "alice" {
		t.Fatalf("trail = %+v", entries)
	}
	if _, page := s.get("/moderation/log"); !contains(page, "deleted an account") {
		t.Error("the log page does not describe the account removal")
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }
