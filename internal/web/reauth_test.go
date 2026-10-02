// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// providerAccount registers an account through the fake provider and returns
// its signed-in browser.
func providerAccount(t *testing.T, h *harness, idp *fakeIDP, subject, username string) *session {
	t.Helper()
	idp.setIdentity(subject, username+"@example.org", true, username)
	client := h.newSession(t)
	resp := h.callback(t, idp, client.client, h.authorize(t, client.client, ""))
	if !strings.HasSuffix(resp.Header.Get("Location"), "/auth/oidc/complete") {
		t.Fatalf("an unknown identity went to %q", resp.Header.Get("Location"))
	}
	if resp, _ := client.post("/auth/oidc/complete", url.Values{"username": {username}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("complete registration = %d", resp.StatusCode)
	}
	if !client.signedIn() {
		t.Fatal("the new provider account is not signed in")
	}
	return client
}

// reauthenticate walks the confirmation round trip, with the provider naming
// subject, and returns the callback response.
func reauthenticate(t *testing.T, h *harness, idp *fakeIDP, s *session, subject, next string) *http.Response {
	t.Helper()
	idp.setIdentity(subject, "", true, "")
	resp, _ := s.post("/settings/reauth", url.Values{"next": {next}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("start confirmation = %d", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return h.callback(t, idp, s.client, location)
}

func TestProviderAccountsHaveNoPasswordToAskFor(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.provisionAdmin("boss")
	s := providerAccount(t, h, idp, "sub-pat", "pat")
	pat, err := h.store.UserByUsername(t.Context(), "pat")
	if err != nil {
		t.Fatal(err)
	}
	if set, err := h.store.PasswordSet(t.Context(), pat.ID); err != nil || set {
		t.Fatalf("a provider account has password_set=%v (%v)", set, err)
	}

	// The settings pages offer confirmation rather than a password field.
	_, page := s.get("/settings/password")
	if !strings.Contains(page, "Set a password") || !strings.Contains(page, `action="/settings/reauth"`) {
		t.Error("the password page does not offer to set one after confirming")
	}
	if strings.Contains(page, `name="current_password"`) {
		t.Error("the password page asks a provider account for a password it never had")
	}

	// Without a recent confirmation, nothing gated by a password goes through.
	resp, page := s.post("/settings/password", url.Values{"password": {"a-new-password"}, "password_confirm": {"a-new-password"}})
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(page, "Confirm it is you") {
		t.Errorf("setting a password without confirming = %d", resp.StatusCode)
	}
	if resp, _ := s.post("/settings/email", url.Values{"email": {"new@example.org"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("changing the address without confirming = %d", resp.StatusCode)
	}
	resp, _ = s.post("/settings/account/delete", url.Values{"confirm": {"pat"}})
	if _, err := h.store.UserByUsername(t.Context(), "pat"); err != nil {
		t.Fatalf("the account was deleted without confirming (%d)", resp.StatusCode)
	}
}

func TestConfirmingAtTheProviderUnlocksSettings(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.provisionAdmin("boss")
	s := providerAccount(t, h, idp, "sub-pat", "pat")

	resp := reauthenticate(t, h, idp, s, "sub-pat", "/settings/password")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/settings/password") {
		t.Fatalf("confirmation = %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if _, page := s.get("/settings/password"); !strings.Contains(page, "confirmed") {
		t.Error("the page does not say the confirmation is in effect")
	}

	resp, _ = s.post("/settings/password", url.Values{"password": {"a-new-password"}, "password_confirm": {"a-new-password"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("setting a first password after confirming = %d", resp.StatusCode)
	}
	pat, err := h.store.UserByUsername(t.Context(), "pat")
	if err != nil {
		t.Fatal(err)
	}
	if set, err := h.store.PasswordSet(t.Context(), pat.ID); err != nil || !set {
		t.Errorf("after setting one, password_set=%v (%v)", set, err)
	}
	if !passwordMatches(pat.PasswordHash, "a-new-password") {
		t.Error("the chosen password does not sign in")
	}
}

// The provider has to name the identity this account is linked to. Being
// signed in at the provider as somebody else proves nothing about this account.
func TestConfirmationNeedsThisAccountsIdentity(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.provisionAdmin("boss")
	s := providerAccount(t, h, idp, "sub-pat", "pat")
	providerAccount(t, h, idp, "sub-other", "other")

	for _, subject := range []string{"sub-other", "sub-unknown"} {
		if resp := reauthenticate(t, h, idp, s, subject, "/settings/password"); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("confirming as %s = %d, want a refusal", subject, resp.StatusCode)
		}
		if resp, _ := s.post("/settings/email", url.Values{"email": {"x@example.org"}}); resp.StatusCode != http.StatusForbidden {
			t.Errorf("after confirming as %s the address changed: %d", subject, resp.StatusCode)
		}
	}
}

// A proof is good for one session and a short while, and is no use forged.
func TestConfirmationIsBoundToSessionAndWindow(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.provisionAdmin("boss")
	s := providerAccount(t, h, idp, "sub-pat", "pat")
	pat, err := h.store.UserByUsername(t.Context(), "pat")
	if err != nil {
		t.Fatal(err)
	}
	reauthenticate(t, h, idp, s, "sub-pat", "/settings/password")

	var proof string
	for _, c := range s.jar.Cookies(mustParse(t, h.server.URL+"/")) {
		if c.Name == reauthCookie {
			proof = c.Value
		}
	}
	if proof == "" {
		t.Fatal("no confirmation cookie was set")
	}

	// Another session for the same account cannot borrow it.
	other := h.sessionFor(t, pat.ID)
	other.jar.SetCookies(mustParse(t, h.server.URL+"/"), []*http.Cookie{{Name: reauthCookie, Value: proof, Path: "/"}})
	if resp, _ := other.post("/settings/email", url.Values{"email": {"x@example.org"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a confirmation from another session was accepted: %d", resp.StatusCode)
	}

	// An expired proof, however well sealed, is refused.
	payload, err := json.Marshal(reauthProof{UserID: pat.ID, Session: sessionDigestFor(t, s), At: time.Now().Add(-reauthWindow - time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := h.srv.secrets.Encrypt(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	s.jar.SetCookies(mustParse(t, h.server.URL+"/"), []*http.Cookie{{Name: reauthCookie, Value: sealed, Path: "/"}})
	if resp, _ := s.post("/settings/email", url.Values{"email": {"x@example.org"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("an expired confirmation was accepted: %d", resp.StatusCode)
	}

	// And a cookie this server did not seal is noise.
	s.jar.SetCookies(mustParse(t, h.server.URL+"/"), []*http.Cookie{{Name: reauthCookie, Value: "forged", Path: "/"}})
	if resp, _ := s.post("/settings/email", url.Values{"email": {"x@example.org"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a forged confirmation was accepted: %d", resp.StatusCode)
	}
}

func sessionDigestFor(t *testing.T, s *session) string {
	t.Helper()
	for _, c := range s.jar.Cookies(mustParse(t, s.h.server.URL+"/")) {
		if c.Name == sessionCookie {
			return hashToken(c.Value)
		}
	}
	t.Fatal("no session cookie")
	return ""
}

// The last way into a passwordless account cannot be removed, and setting a
// password lifts that.
func TestTheLastWayInCannotBeDisconnected(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.provisionAdmin("boss")
	s := providerAccount(t, h, idp, "sub-pat", "pat")
	pat, err := h.store.UserByUsername(t.Context(), "pat")
	if err != nil {
		t.Fatal(err)
	}
	identities, err := h.store.IdentitiesByUser(t.Context(), pat.ID)
	if err != nil || len(identities) != 1 {
		t.Fatalf("identities = %v (%v)", identities, err)
	}
	path := "/settings/account/identities/" + itoa64(identities[0].ID) + "/delete"

	s.post(path, url.Values{})
	if after, _ := h.store.IdentitiesByUser(t.Context(), pat.ID); len(after) != 1 {
		t.Fatal("the only way into a passwordless account was disconnected")
	}

	reauthenticate(t, h, idp, s, "sub-pat", "/settings/password")
	if resp, _ := s.post("/settings/password", url.Values{"password": {"a-new-password"}, "password_confirm": {"a-new-password"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("set password = %d", resp.StatusCode)
	}
	s = h.sessionFor(t, pat.ID)
	s.post(path, url.Values{})
	if after, _ := h.store.IdentitiesByUser(t.Context(), pat.ID); len(after) != 0 {
		t.Error("with a password set, the connection could not be removed")
	}
}

func TestReauthRouteContract(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)

	// Signed out: sent to sign in.
	anon := h.newSession(t)
	if resp, _ := anon.post("/settings/reauth", url.Values{}); resp.StatusCode != http.StatusSeeOther ||
		!strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Errorf("signed-out confirmation = %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Only POST, and only with the token.
	user := h.seedUser("plain")
	s := h.sessionFor(t, user.ID)
	if resp, _ := s.get("/settings/reauth"); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /settings/reauth = %d, want 405", resp.StatusCode)
	}
	resp := doForm(t, s.client, h.server.URL, "/settings/reauth", url.Values{})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("confirmation without a token = %d, want 403", resp.StatusCode)
	}

	// An account with nothing connected is told so, and an off-site next is
	// not followed.
	resp, _ = s.post("/settings/reauth", url.Values{"next": {"//evil.example"}})
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/settings/password") {
		t.Errorf("confirmation with nothing connected = %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}
