// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"imvault/internal/invites"
)

// A member's invitations are bounded; an administrator's are not.
func TestMemberInvitationsAreCapped(t *testing.T) {
	h, _, member, user := delegatedInvitationHarness(t)

	for _, tc := range []struct {
		uses, days string
		refused    bool
	}{
		{"0", "7", true},  // unlimited is an administrator's call
		{"26", "7", true}, // over the use cap
		{"-1", "7", true}, // nonsense
		{"5", "31", true}, // over the lifetime cap
		{"5", "0", true},  // never expiring
		{"25", "30", false},
		{"1", "", false}, // blank expiry defaults to the cap
		{"", "1", false}, // blank uses defaults to one
	} {
		resp, page := member.post("/invites", url.Values{"max_uses": {tc.uses}, "expires_days": {tc.days}})
		minted := strings.Contains(page, `aria-label="Invitation code"`)
		if resp.StatusCode != http.StatusOK || minted == tc.refused {
			t.Errorf("uses=%q days=%q: status %d, minted %v, want refused %v", tc.uses, tc.days, resp.StatusCode, minted, tc.refused)
		}
	}

	list, _, err := h.store.ListInvitesByCreator(t.Context(), user.ID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("member minted %d invitations, want 3", len(list))
	}
	limit := time.Now().AddDate(0, 0, memberMaxInviteDays+1)
	for _, inv := range list {
		if inv.Unlimited() || inv.MaxUses > memberMaxInviteUses {
			t.Errorf("member invitation allows %d uses", inv.MaxUses)
		}
		if inv.ExpiresAt == nil || inv.ExpiresAt.After(limit) {
			t.Errorf("member invitation expires %v, want within %d days", inv.ExpiresAt, memberMaxInviteDays)
		}
	}

	// The form says what the bounds are.
	_, page := member.get("/invites")
	if !strings.Contains(page, `max="25"`) || !strings.Contains(page, "1 to 30") {
		t.Error("the member form does not state its bounds")
	}
}

func TestAdministratorInvitationsAreNotCapped(t *testing.T) {
	_, owner, _, _ := delegatedInvitationHarness(t)
	resp, page := owner.post("/admin/invites", url.Values{"max_uses": {"0"}, "expires_days": {""}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, `aria-label="Invitation code"`) {
		t.Fatalf("administrator unlimited invitation = %d: %s", resp.StatusCode, truncate(page))
	}
	if !strings.Contains(page, "0 for no limit") {
		t.Error("the administrator form lost its unlimited option")
	}
}

func TestRemovingInviteAccessSaysOpenCodesAreRevoked(t *testing.T) {
	_, owner, _, user := delegatedInvitationHarness(t)
	resp, _ := owner.post("/admin/users/"+itoa64(user.ID)+"/invites", url.Values{"can_invite": {"0"}})
	_, page := owner.get(resp.Header.Get("Location"))
	if !strings.Contains(page, "open invitations were revoked") {
		t.Error("removing invitation access does not say what happens to open codes")
	}
}

func TestInvitationListIsPaginated(t *testing.T) {
	h, owner, _, _ := delegatedInvitationHarness(t)
	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < invitesPageSize+3; i++ {
		generated := invites.Generate()
		if _, err := h.store.CreateInvite(t.Context(), boss.ID, "bulk", generated.Prefix, generated.Hash, 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	_, first := owner.get("/admin/invites")
	if strings.Count(first, "<td><code>") != invitesPageSize || !strings.Contains(first, "page=2") {
		t.Errorf("first page lists %d codes, want %d and a link onwards", strings.Count(first, "<td><code>"), invitesPageSize)
	}
	_, second := owner.get("/admin/invites?page=2")
	if got := strings.Count(second, "<td><code>"); got != 3 {
		t.Errorf("second page lists %d codes, want 3", got)
	}
}

// The merged handlers keep both route families: administrators under /admin,
// delegated members under /invites, each gated.
func TestInvitationRoutesExist(t *testing.T) {
	h, owner, member, _ := delegatedInvitationHarness(t)
	outsider := h.sessionFor(t, h.seedUser("outsider").ID)
	for _, tc := range []struct {
		who  *session
		path string
		want int
	}{
		{owner, "/admin/invites", http.StatusOK},
		{owner, "/invites", http.StatusOK},
		{member, "/invites", http.StatusOK},
		{member, "/admin/invites", http.StatusForbidden},
		{outsider, "/invites", http.StatusForbidden},
	} {
		if resp, _ := tc.who.get(tc.path); resp.StatusCode != tc.want {
			t.Errorf("GET %s = %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
	}
	if resp, _ := member.post("/admin/invites", url.Values{}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("member POST /admin/invites = %d, want 403", resp.StatusCode)
	}
	if resp, _ := member.post("/invites/abc/revoke", url.Values{}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("revoke with a bad id = %d, want 400", resp.StatusCode)
	}
}
