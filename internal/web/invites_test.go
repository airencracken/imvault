// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"imvault/internal/invites"
	"imvault/internal/models"
)

// seedInvite mints a code straight into the store, which is what an
// administrator's "create" button does.
func (h *harness) seedInvite(t *testing.T, maxUses int, expiresAt *time.Time) (string, *models.Invite) {
	t.Helper()

	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatalf("the harness account is missing: %v", err)
	}

	generated := invites.Generate()
	inv, err := h.store.CreateInvite(t.Context(), boss.ID, "test", generated.Prefix,
		generated.Hash, maxUses, expiresAt)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	return generated.Full, inv
}

func TestDelegatedInvitesAreGrantedScopedAndAttributed(t *testing.T) {
	h := newHarness(t)
	admin := h.provisionAdmin("boss")
	delegate := h.seedUser("jules")
	adminSession := h.sessionFor(t, admin.ID)
	delegateSession := h.sessionFor(t, delegate.ID)

	if resp, _ := delegateSession.get("/invites"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ungranted user invitation page = %d, want 403", resp.StatusCode)
	}
	resp, _ := adminSession.post("/admin/users/"+itoa64(delegate.ID)+"/invites", url.Values{"can_invite": {"1"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("grant invite permission = %d", resp.StatusCode)
	}
	delegateSession = h.sessionFor(t, delegate.ID)
	if resp, page := delegateSession.get("/invites"); resp.StatusCode != http.StatusOK || !strings.Contains(page, "Create an invitation") {
		t.Fatalf("granted user invitation page = %d, body %s", resp.StatusCode, truncate(page))
	}

	codeResp, codePage := delegateSession.post("/invites", url.Values{"label": {"From Jules"}, "max_uses": {"1"}, "expires_days": {"7"}})
	if codeResp.StatusCode != http.StatusOK {
		t.Fatalf("delegated invite creation = %d: %s", codeResp.StatusCode, truncate(codePage))
	}
	match := regexp.MustCompile(`value="(inv_[A-Za-z0-9_-]+)"`).FindStringSubmatch(codePage)
	if len(match) != 2 {
		t.Fatalf("new invite code missing: %s", truncate(codePage))
	}
	list, total, err := h.store.ListInvitesByCreator(t.Context(), delegate.ID, 10, 0)
	if err != nil || total != 1 || len(list) != 1 || list[0].Label != "From Jules" {
		t.Fatalf("delegated invitation list: %+v total=%d err=%v", list, total, err)
	}

	guest := h.newSession(t)
	resp, body := guest.post("/register", url.Values{"username": {"sam"}, "password": {testPassword}, "invite": {match[1]}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("invite registration = %d: %s", resp.StatusCode, truncate(body))
	}
	joined, err := h.store.UserByUsername(t.Context(), "sam")
	if err != nil || joined.InvitedBy == nil || *joined.InvitedBy != delegate.ID || joined.InvitedByUsername != "jules" || joined.InvitationID == nil || *joined.InvitationID != list[0].ID {
		t.Fatalf("new account attribution: %+v err=%v", joined, err)
	}

	adminCode, adminInvite := h.seedInvite(t, 1, nil)
	if strings.Contains(codePage, adminCode) {
		t.Fatal("member invitation response exposed an administrator's code")
	}
	_, memberInvites := delegateSession.get("/invites")
	if strings.Contains(memberInvites, adminInvite.Prefix) {
		t.Fatal("member invitation list included another creator's invitation")
	}
	_, _ = delegateSession.post("/invites/"+itoa64(adminInvite.ID)+"/revoke", url.Values{})
	if current, err := h.store.InviteByID(t.Context(), adminInvite.ID); err != nil || current.Revoked() {
		t.Fatalf("delegated user revoked another person's invitation: %+v err=%v", current, err)
	}
}

// registerWith submits the registration form as given.
func (h *harness) registerWith(fields url.Values) (*http.Response, string) {
	h.t.Helper()

	fields.Set("csrf_token", h.csrf())
	if _, ok := fields["password"]; !ok {
		fields.Set("password", testPassword)
	}
	return h.postForm("/register", fields)
}

func TestInvitationOnlyRegistrationNeedsACode(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	h.setPolicy(t, models.Settings{
		AllowSignup:           true,
		InviteOnly:            true,
		AllowAnonymousUploads: true,
		DefaultVisibility:     models.VisibilityMembers,
	})

	// The page asks for one.
	_, page := h.newSession(t).get("/register")
	if !strings.Contains(page, `name="invite"`) {
		t.Error("the registration page does not offer an invitation field")
	}
	if !strings.Contains(page, "by invitation") {
		t.Error("the registration page does not explain that it is by invitation")
	}

	// Without a code, registration is refused.
	resp, _ := h.registerWith(url.Values{"username": {"nobody"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("registration without a code = %d, want 403", resp.StatusCode)
	}
	if _, err := h.store.UserByUsername(t.Context(), "nobody"); err == nil {
		t.Error("an account was created without an invitation")
	}

	// So is a code that does not exist, and one whose secret is wrong.
	resp, _ = h.registerWith(url.Values{"username": {"nobody"}, "invite": {"inv_aaaaaaaaaaaa_" + strings.Repeat("b", 32)}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("registration with an unknown code = %d, want 403", resp.StatusCode)
	}

	code, inv := h.seedInvite(t, 1, nil)
	tampered := code[:len(code)-1]
	if strings.HasSuffix(code, "z") {
		tampered += "y"
	} else {
		tampered += "z"
	}
	resp, _ = h.registerWith(url.Values{"username": {"nobody"}, "invite": {tampered}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("registration with a tampered code = %d, want 403", resp.StatusCode)
	}

	// The real thing works.
	resp, _ = h.registerWith(url.Values{"username": {"invited"}, "invite": {code}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("registration with a valid code = %d, want 303", resp.StatusCode)
	}
	if _, err := h.store.UserByUsername(t.Context(), "invited"); err != nil {
		t.Errorf("the invited account was not created: %v", err)
	}

	after, err := h.store.InviteByID(t.Context(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Uses != 1 {
		t.Errorf("uses = %d, want 1", after.Uses)
	}
	if after.Usable(time.Now()) {
		t.Error("a spent single-use invitation is still usable")
	}
}

func TestAnInvitationAdmitsEvenWhenRegistrationIsClosed(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	h.setPolicy(t, models.Settings{
		AllowSignup:           false,
		InviteOnly:            false,
		AllowAnonymousUploads: false,
		DefaultVisibility:     models.VisibilityPrivate,
	})

	// Closed means closed to the public.
	resp, _ := h.registerWith(url.Values{"username": {"stranger"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("registration while closed = %d, want 403", resp.StatusCode)
	}

	// But an invitation is a deliberate grant, so it still admits. This is what
	// makes "close the door, then invite the people you want" possible.
	code, _ := h.seedInvite(t, 1, nil)
	resp, _ = h.registerWith(url.Values{"username": {"friend"}, "invite": {code}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("an invitation was refused while closed: %d", resp.StatusCode)
	}
	if _, err := h.store.UserByUsername(t.Context(), "friend"); err != nil {
		t.Errorf("the invited account was not created: %v", err)
	}
}

func TestAFailedRegistrationDoesNotBurnTheInvitation(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	h.setPolicy(t, models.Settings{
		AllowSignup:           true,
		InviteOnly:            true,
		AllowAnonymousUploads: true,
		DefaultVisibility:     models.VisibilityMembers,
	})

	code, inv := h.seedInvite(t, 1, nil)

	// A taken username must not cost the code a use: the account and the use
	// are one transaction for exactly this reason.
	resp, _ := h.registerWith(url.Values{"username": {"boss"}, "invite": {code}})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a duplicate username = %d, want 409", resp.StatusCode)
	}
	after, err := h.store.InviteByID(t.Context(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Uses != 0 {
		t.Errorf("uses = %d after a refused registration, want 0", after.Uses)
	}
	if !after.Usable(time.Now()) {
		t.Error("the invitation was spent by a registration that did not happen")
	}

	// And it still works for a name that is free.
	resp, _ = h.registerWith(url.Values{"username": {"newcomer"}, "invite": {code}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("the code no longer works: %d", resp.StatusCode)
	}
}

func TestALimitedInvitationRunsOut(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	h.setPolicy(t, models.Settings{
		AllowSignup:           true,
		InviteOnly:            true,
		AllowAnonymousUploads: true,
		DefaultVisibility:     models.VisibilityMembers,
	})

	code, _ := h.seedInvite(t, 2, nil)

	for _, username := range []string{"first", "second"} {
		resp, _ := h.registerWith(url.Values{"username": {username}, "invite": {code}})
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("%s = %d, want 303", username, resp.StatusCode)
		}
	}

	resp, _ := h.registerWith(url.Values{"username": {"third"}, "invite": {code}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a third use of a two-use code = %d, want 403", resp.StatusCode)
	}
	if _, err := h.store.UserByUsername(t.Context(), "third"); err == nil {
		t.Error("the third account was created anyway")
	}
}

func TestRevokedAndExpiredInvitationsAreRefused(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	h.setPolicy(t, models.Settings{
		AllowSignup:           true,
		InviteOnly:            true,
		AllowAnonymousUploads: true,
		DefaultVisibility:     models.VisibilityMembers,
	})

	past := time.Now().Add(-time.Hour)
	expiredCode, _ := h.seedInvite(t, 1, &past)
	resp, _ := h.registerWith(url.Values{"username": {"toolate"}, "invite": {expiredCode}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("an expired invitation = %d, want 403", resp.StatusCode)
	}

	code, inv := h.seedInvite(t, 1, nil)
	if err := h.store.RevokeInvite(t.Context(), inv.ID); err != nil {
		t.Fatal(err)
	}
	resp, _ = h.registerWith(url.Values{"username": {"revoked"}, "invite": {code}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a revoked invitation = %d, want 403", resp.StatusCode)
	}
}

func TestOnlyAnAdministratorManagesInvitations(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	mod := h.seedUser("mod")
	member := h.seedUser("member")
	if err := h.store.SetUserRole(t.Context(), mod.ID, models.RoleModerator); err != nil {
		t.Fatal(err)
	}

	_, inv := h.seedInvite(t, 1, nil)

	for _, who := range []struct {
		name   string
		client *session
	}{
		{"moderator", h.sessionFor(t, mod.ID)},
		{"member", h.sessionFor(t, member.ID)},
	} {
		if resp, _ := who.client.get("/admin/invites"); resp.StatusCode != http.StatusForbidden {
			t.Errorf("a %s reached the invitation list: %d", who.name, resp.StatusCode)
		}
		resp, _ := who.client.post("/admin/invites", url.Values{"max_uses": {"5"}})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("a %s created an invitation: %d", who.name, resp.StatusCode)
		}
		resp, _ = who.client.post("/admin/invites/"+itoa64(inv.ID)+"/revoke", url.Values{})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("a %s revoked an invitation: %d", who.name, resp.StatusCode)
		}
	}

	// None of that changed anything.
	after, err := h.store.InviteByID(t.Context(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revoked() {
		t.Error("an invitation was revoked by somebody who may not")
	}
	if after.Uses != 0 {
		t.Errorf("uses = %d", after.Uses)
	}
}

func TestACodeIsShownOnceAndThenOnlyItsPrefix(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	resp, body := h.postHTMX("/admin/invites", url.Values{
		"csrf_token": {h.csrf()},
		"label":      {"for Sam"},
		"max_uses":   {"3"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "inv_") || !strings.Contains(body, "cannot be shown again") {
		t.Errorf("the created code was not revealed: %s", truncate(body))
	}

	// The code is in the response body, but only its prefix is stored, so the
	// page never shows it again.
	_, page := h.get("/admin/invites")
	if !strings.Contains(page, "for Sam") {
		t.Error("the new invitation is not listed")
	}
	if strings.Contains(page, "cannot be shown again") {
		t.Error("the page still claims to reveal a code")
	}

	list, _, err := h.store.ListInvites(t.Context(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("%d invitations stored, want 1", len(list))
	}
	if !strings.Contains(page, list[0].Prefix) {
		t.Error("the invitation's prefix is not listed")
	}
	// The whole code is not recoverable from a later page load, which is the
	// point of storing a digest rather than the code.
	if strings.Contains(page, list[0].Prefix+"_") {
		t.Error("the page can be made to show the whole code again")
	}
}

func TestInviteOnlyIsAnInstanceSetting(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	if h.srv.policy().InviteOnly {
		t.Fatal("the harness starts invitation-only")
	}

	// Turn it on from the settings page, the way an administrator would.
	resp, _ := h.postForm("/admin/settings", url.Values{
		"csrf_token":         {h.csrf()},
		"allow_signup":       {"1"},
		"invite_only":        {"1"},
		"anonymous_ttl":      {"24h"},
		"default_visibility": {"members"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save settings = %d, want 303", resp.StatusCode)
	}
	if !h.srv.policy().InviteOnly {
		t.Fatal("invite_only was not saved")
	}

	// The page says how to get in, and registration is now gated.
	_, page := h.get("/admin/settings")
	if !strings.Contains(page, `name="invite_only"`) {
		t.Error("the settings page has no invitation switch")
	}
	_, register := h.newSession(t).get("/register")
	if !strings.Contains(register, "by invitation") {
		t.Error("the registration page does not say it is by invitation")
	}

	resp, _ = h.registerWith(url.Values{"username": {"walkin"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("registration = %d, want 403 now that invitations are required", resp.StatusCode)
	}

	// Clearing the settings puts the configuration back in charge.
	if resp, _ := h.postForm("/admin/settings/clear", url.Values{"csrf_token": {h.csrf()}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("clear = %d", resp.StatusCode)
	}
	if h.srv.policy().InviteOnly {
		t.Error("clearing did not restore the configuration")
	}
}
