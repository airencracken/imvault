// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
	"imvault/internal/store"
)

// plantedSession is a signed-in browser whose CSRF cookie was set by somebody
// else, such as a neighbouring subdomain.
func plantedSession(t *testing.T, h *harness, userID int64, planted string) *session {
	t.Helper()
	s := h.sessionFor(t, userID)
	s.jar.SetCookies(mustParse(t, h.server.URL+"/"), []*http.Cookie{{Name: csrfCookie, Value: planted, Path: "/"}})
	s.csrf = planted
	return s
}

// A token the attacker chose is useless against a signed-in session, even
// when cookie and field agree.
func TestPlantedCSRFCookieIsRefusedForASession(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	planted := strings.Repeat("a", 40)
	s := plantedSession(t, h, alice.ID, planted)

	form := url.Values{"title": {"Planted"}, "csrf_token": {planted}}
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/albums", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	closeBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a planted token was accepted: %d", resp.StatusCode)
	}

	// The next page load hands the browser the token its session expects.
	if resp, _ := s.get("/gallery"); resp.StatusCode != http.StatusOK {
		t.Fatal("gallery unavailable")
	}
	if got := s.token(); got == planted {
		t.Fatal("the planted cookie was not replaced")
	}
	if resp, _ := s.post("/albums", url.Values{"title": {"Real"}}); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("the reissued token was refused: %d", resp.StatusCode)
	}
}

// Signing in swaps the signed-out token for one bound to the new session, so
// a token learned before sign-in does not carry over.
func TestSigningInReplacesTheToken(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	s := h.newSession(t)
	before := s.token()
	if resp, _ := s.post("/login", url.Values{"username": {"boss"}, "password": {testPassword}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	after := s.token()
	if after == before || after != sessionCSRFToken(sessionValue(t, s)) {
		t.Fatal("signing in did not issue a session-bound token")
	}

	form := url.Values{"title": {"Stale"}, "csrf_token": {before}}
	resp := doForm(t, s.client, h.server.URL, "/albums", form)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("the signed-out token still works after signing in: %d", resp.StatusCode)
	}
}

func sessionValue(t *testing.T, s *session) string {
	t.Helper()
	for _, c := range s.jar.Cookies(mustParse(t, s.h.server.URL+"/")) {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatal("no session cookie")
	return ""
}

// Turning on a second factor ends every earlier session and continues in a
// fresh one, whose token the page it renders already carries.
func TestEnablingTwoFactorRotatesTheSession(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	s := h.sessionFor(t, alice.ID)
	other := h.sessionFor(t, alice.ID)
	before := sessionValue(t, s)

	if resp, _ := s.post("/settings/2fa/begin", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("begin = %d", resp.StatusCode)
	}
	resp, page := s.post("/settings/2fa/confirm", url.Values{"code": {h.totpCodeFor(t, alice.ID)}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "Save your recovery codes") {
		t.Fatalf("confirm = %d", resp.StatusCode)
	}

	after := sessionValue(t, s)
	if after == before {
		t.Fatal("the session was not rotated")
	}
	if !strings.Contains(page, sessionCSRFToken(after)) {
		t.Error("the rendered page carries a token for the old session")
	}
	if other.signedIn() {
		t.Error("a session that never saw the second factor survived it")
	}
	if !s.signedIn() {
		t.Error("the caller was signed out by its own rotation")
	}
	if resp, _ := s.post("/albums", url.Values{"title": {"After"}}); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("the rotated session cannot post: %d", resp.StatusCode)
	}
}

// The code that confirmed enrolment cannot be replayed to sign in.
func TestTheEnrolmentCodeIsSpent(t *testing.T) {
	h := newHarness(t)
	alice, err := h.store.CreateUser(t.Context(), store.NewUser{
		Username: "alice", PasswordHash: mustHashPassword(t, testPassword), Role: models.RoleMember,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := h.sessionFor(t, alice.ID)
	s.post("/settings/2fa/begin", url.Values{})
	code := h.totpCodeFor(t, alice.ID)
	if resp, _ := s.post("/settings/2fa/confirm", url.Values{"code": {code}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm = %d", resp.StatusCode)
	}

	fresh := h.newSession(t)
	if resp, _ := fresh.signInTo("alice", testPassword, code); resp.StatusCode == http.StatusSeeOther && fresh.signedIn() {
		t.Error("the enrolment code signed in a second time")
	}
}
