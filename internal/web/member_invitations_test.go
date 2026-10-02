// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"imvault/internal/models"
)

func delegatedInvitationHarness(t *testing.T) (*harness, *session, *session, *models.User) {
	t.Helper()
	h := newHarness(t)
	admin := h.provisionAdmin("boss")
	member := h.seedUser("jules")
	owner, delegate := h.sessionFor(t, admin.ID), h.sessionFor(t, member.ID)
	if resp, _ := owner.post("/admin/users/"+itoa64(member.ID)+"/invites", url.Values{"can_invite": {"1"}}); resp.StatusCode != 303 {
		t.Fatal("could not grant invitations")
	}
	return h, owner, delegate, member
}

func TestMemberInvitationNavigationAndLiveRevocation(t *testing.T) {
	h, owner, member, user := delegatedInvitationHarness(t)
	if resp, body := member.get("/gallery"); resp.StatusCode != 200 || !strings.Contains(body, `href="/invites"`) {
		t.Fatalf("grant missing from existing session's navigation: %d", resp.StatusCode)
	}
	if _, body := owner.get("/admin/users"); !strings.Contains(body, "can invite") || !strings.Contains(body, "Remove invitation access for jules") {
		t.Fatal("grant has no visible or accessible account status")
	}
	if resp, _ := owner.post("/admin/users/"+itoa64(user.ID)+"/invites", url.Values{"can_invite": {"0"}}); resp.StatusCode != 303 {
		t.Fatal("could not remove invitations")
	}
	if _, body := member.get("/gallery"); strings.Contains(body, `href="/invites"`) {
		t.Fatal("revoked permission remains in navigation")
	}
	_, invitation := h.seedInvite(t, 1, nil)
	for _, path := range []string{"/invites", "/invites/" + itoa64(invitation.ID) + "/revoke"} {
		if resp, _ := member.post(path, url.Values{}); resp.StatusCode != 403 {
			t.Fatalf("revoked member reached %s: %d", path, resp.StatusCode)
		}
	}
	if resp, _ := member.get("/invites"); resp.StatusCode != 403 {
		t.Fatalf("revoked member reached invitation page: %d", resp.StatusCode)
	}
}

func TestMemberInvitationResponsesWorkWithAndWithoutHTMX(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "page", true: "fragment"}[partial], func(t *testing.T) {
			h, _, member, user := delegatedInvitationHarness(t)
			post := func(path string, fields url.Values) (*http.Response, string) {
				t.Helper()
				request, err := http.NewRequest("POST", h.server.URL+path, strings.NewReader(fields.Encode()))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				request.Header.Set("X-CSRF-Token", member.csrf)
				if partial {
					request.Header.Set("HX-Request", "true")
				}
				response, err := member.client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer mustClose(t, response.Body)
				return response, readAll(t, response.Body)
			}
			label := `Family <script>alert("x")</script>`
			resp, body := post("/invites", url.Values{"label": {label}, "max_uses": {"2"}, "expires_days": {"7"}})
			if resp.StatusCode != 200 || strings.Contains(body, "<!doctype html>") == partial || !strings.Contains(body, `aria-label="Invitation link"`) {
				t.Fatalf("incorrect invitation response: %d, %s", resp.StatusCode, truncate(body))
			}
			if resp.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("one-time invitation can be cached")
			}
			if strings.Contains(body, label) {
				t.Fatal("invitation label was not escaped")
			}
			code := regexp.MustCompile(`value="(inv_[A-Za-z0-9_-]+)"`).FindStringSubmatch(body)
			if len(code) != 2 || !strings.Contains(body, "/register?invite="+code[1]) {
				t.Fatal("new invitation has no usable code and link")
			}
			if !partial && !strings.Contains(body, `href="/invites"`) {
				t.Fatal("ordinary form submission lost navigation")
			}
			list, _, err := h.store.ListInvitesByCreator(t.Context(), user.ID, 10, 0)
			if err != nil || len(list) != 1 || list[0].MaxUses != 2 || list[0].ExpiresAt == nil {
				t.Fatalf("incorrect stored invitation: %+v, %v", list, err)
			}
			_, reload := member.get("/invites")
			if strings.Contains(reload, code[1]) {
				t.Fatal("reload revealed full invitation again")
			}
			resp, body = post("/invites/"+itoa64(list[0].ID)+"/revoke", url.Values{})
			if resp.StatusCode != 200 || strings.Contains(body, "<!doctype html>") == partial || !strings.Contains(body, "revoked") {
				t.Fatalf("incorrect revocation response: %d, %s", resp.StatusCode, truncate(body))
			}
		})
	}
}

func TestInvitationPermissionRejectsAdversarialFormsAtomically(t *testing.T) {
	h, owner, delegate, user := delegatedInvitationHarness(t)
	path := "/admin/users/" + itoa64(user.ID) + "/invites"
	for _, values := range [][]string{nil, {""}, {"true"}, {"-1"}, {"2"}, {"1\n"}, {"0", "1"}, {"1", "1"}} {
		resp, _ := owner.post(path, url.Values{"can_invite": values})
		if resp.StatusCode != 400 {
			t.Fatalf("invalid grant %q accepted: %d", values, resp.StatusCode)
		}
		after, err := h.store.UserByID(t.Context(), user.ID)
		if err != nil || !after.CanInvite {
			t.Fatalf("invalid grant changed permission: %+v, %v", after, err)
		}
	}
	if resp, _ := delegate.post(path, url.Values{"can_invite": {"0"}}); resp.StatusCode != 403 {
		t.Fatalf("member changed own permission: %d", resp.StatusCode)
	}
}

func TestMemberInvitationValidationKeepsFieldsWithoutCreatingCode(t *testing.T) {
	h, _, member, user := delegatedInvitationHarness(t)
	for _, fields := range []url.Values{
		{"label": {"For Sam"}, "max_uses": {"-1"}, "expires_days": {"7"}},
		{"label": {"For Sam"}, "max_uses": {"2"}, "expires_days": {"invalid"}},
	} {
		resp, body := member.post("/invites", fields)
		if resp.StatusCode != 200 || !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, `value="For Sam"`) || !strings.Contains(body, `value="`+fields.Get("max_uses")+`"`) || !strings.Contains(body, `value="`+fields.Get("expires_days")+`"`) || strings.Count(body, `class="flash err"`) != 1 {
			t.Fatalf("validation lost fields or duplicated error: %d, %s", resp.StatusCode, truncate(body))
		}
	}
	_, total, err := h.store.ListInvitesByCreator(t.Context(), user.ID, 10, 0)
	if err != nil || total != 0 {
		t.Fatalf("invalid form created code: %d, %v", total, err)
	}
}

func TestInvitationPageReportsRegistrationPolicy(t *testing.T) {
	h, owner, _, _ := delegatedInvitationHarness(t)
	for _, state := range []struct{ signup, required bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		h.setPolicy(t, models.Settings{AllowSignup: state.signup, InviteOnly: state.required, DefaultVisibility: models.VisibilityPrivate})
		resp, body := owner.get("/admin/invites")
		if resp.StatusCode != 200 || strings.Contains(body, "Registration is currently") != (state.signup && !state.required) {
			t.Fatalf("misleading registration policy: %+v, %d, %s", state, resp.StatusCode, truncate(body))
		}
		if !strings.Contains(body, `href="/admin/users"`) || !strings.Contains(body, "Allow invites") {
			t.Fatal("invitation page does not explain delegation")
		}
	}
}

func TestDisabledMemberInvitationCannotRegister(t *testing.T) {
	h, _, member, user := delegatedInvitationHarness(t)
	_, body := member.post("/invites", url.Values{"max_uses": {"1"}})
	code := regexp.MustCompile(`value="(inv_[A-Za-z0-9_-]+)"`).FindStringSubmatch(body)
	if len(code) != 2 {
		t.Fatal("missing code")
	}
	if err := h.store.SetUserDisabled(t.Context(), user.ID, true); err != nil {
		t.Fatal(err)
	}
	guest := h.newSession(t)
	resp, _ := guest.post("/register", url.Values{"username": {"outsider"}, "password": {testPassword}, "invite": {code[1]}})
	if resp.StatusCode != 403 {
		t.Fatalf("disabled issuer admitted account: %d", resp.StatusCode)
	}
	list, _, err := h.store.ListInvitesByCreator(t.Context(), user.ID, 10, 0)
	if err != nil || len(list) != 1 || list[0].Uses != 0 {
		t.Fatalf("failed registration spent code: %+v, %v", list, err)
	}
	if _, err := h.store.UserByUsername(t.Context(), "outsider"); err == nil {
		t.Fatal("failed registration left account")
	}
}

func TestMemberInvitationRoutesRequireCSRFAndAdministratorGrant(t *testing.T) {
	h, owner, member, user := delegatedInvitationHarness(t)
	_, invitation := h.seedInvite(t, 1, nil)
	for _, path := range []string{"/invites", "/invites/" + itoa64(invitation.ID) + "/revoke"} {
		request, err := http.NewRequest("POST", h.server.URL+path, strings.NewReader("max_uses=1"))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := member.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		mustClose(t, resp.Body)
		if resp.StatusCode != 403 {
			t.Fatalf("missing CSRF reached %s: %d", path, resp.StatusCode)
		}
	}
	if resp, _ := member.post("/admin/users/"+itoa64(user.ID)+"/invites", url.Values{"can_invite": {"1"}}); resp.StatusCode != 403 {
		t.Fatalf("member granted invitation access: %d", resp.StatusCode)
	}
	if resp, _ := owner.post("/admin/users/999999/invites", url.Values{"can_invite": {"1"}}); resp.StatusCode != 404 {
		t.Fatalf("missing target reported success: %d", resp.StatusCode)
	}
	_, total, err := h.store.ListInvitesByCreator(t.Context(), user.ID, 10, 0)
	if err != nil || total != 0 {
		t.Fatalf("denied requests created invitations: %d, %v", total, err)
	}
	after, err := h.store.InviteByID(t.Context(), invitation.ID)
	if err != nil || after.Revoked() {
		t.Fatalf("denied requests revoked invitation: %+v, %v", after, err)
	}
}
