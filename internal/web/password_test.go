// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"imvault/internal/store"
	"imvault/internal/tokens"
)

func TestPasswordResetByEmail(t *testing.T) {
	h := newMailHarness(t)

	// Seed an account with a known password and a real address.
	user := h.seedUser("alice")
	hash := mustHashPassword(t, "original-password")
	if err := h.store.SetPassword(t.Context(), user.ID, hash); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetEmail(t.Context(), user.ID, "alice@example.com", false); err != nil {
		t.Fatal(err)
	}

	// Ask for a reset.
	h.get("/")
	resp, _ := h.postForm("/forgot", url.Values{
		"csrf_token": {h.csrf()},
		"identifier": {"alice"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("forgot = %d, want 200", resp.StatusCode)
	}

	msg := h.mailer.last(t)
	if msg.To != "alice@example.com" {
		t.Errorf("message went to %q, want the account's address", msg.To)
	}
	if !strings.Contains(msg.Subject, "password") {
		t.Errorf("subject = %q", msg.Subject)
	}
	token := resetTokenFrom(t, msg)

	// The form is reachable without spending the token, even twice over.
	for i := 0; i < 2; i++ {
		page, body := h.get("/reset/" + token)
		if page.StatusCode != http.StatusOK {
			t.Fatalf("reset page %d = %d, want 200", i+1, page.StatusCode)
		}
		if !strings.Contains(body, "Choose a new password") {
			t.Errorf("reset page %d is missing the form", i+1)
		}
	}

	// A mismatch is refused without consuming the token.
	resp, _ = h.postForm("/reset/"+token, url.Values{
		"csrf_token":       {h.csrf()},
		"password":         {"new-password-1"},
		"password_confirm": {"new-password-2"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("mismatched confirm = %d, want 400", resp.StatusCode)
	}

	// Too-short is refused too.
	resp, _ = h.postForm("/reset/"+token, url.Values{
		"csrf_token":       {h.csrf()},
		"password":         {"short"},
		"password_confirm": {"short"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("short password = %d, want 400", resp.StatusCode)
	}

	// Now do it properly.
	resp, _ = h.postForm("/reset/"+token, url.Values{
		"csrf_token":       {h.csrf()},
		"password":         {"brand-new-password"},
		"password_confirm": {"brand-new-password"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset = %d, want 303", resp.StatusCode)
	}

	// The new password works.
	updated, err := h.store.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !passwordMatches(updated.PasswordHash, "brand-new-password") {
		t.Error("the new password does not verify against the stored hash")
	}
	if passwordMatches(updated.PasswordHash, "original-password") {
		t.Error("the old password still works")
	}

	// The token is single use.
	resp, _ = h.postForm("/reset/"+token, url.Values{
		"csrf_token":       {h.csrf()},
		"password":         {"another-password"},
		"password_confirm": {"another-password"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("reusing a token = %d, want 400", resp.StatusCode)
	}
}

func TestForgotDoesNotRevealWhetherAnAccountExists(t *testing.T) {
	h := newMailHarness(t)
	h.get("/")

	_, existing := h.postForm("/forgot", url.Values{
		"csrf_token": {h.csrf()},
		"identifier": {"alice"},
	})
	token := h.csrf()
	_, missing := h.postForm("/forgot", url.Values{
		"csrf_token": {token},
		"identifier": {"nobody-here"},
	})

	// Both responses must be indistinguishable, or the endpoint enumerates
	// accounts.
	existingText := strings.Join(strings.Fields(existing), " ")
	missingText := strings.Join(strings.Fields(missing), " ")
	if existingText != missingText {
		t.Errorf("responses differ:\nexisting: %s\nmissing:  %s", truncate(existingText), truncate(missingText))
	}
}

func TestResetRejectsBadAndExpiredTokens(t *testing.T) {
	h := newMailHarness(t)

	user := h.seedUser("alice")

	// Unknown token.
	if resp, _ := h.get("/reset/nosuchtoken"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown token page = %d, want 400", resp.StatusCode)
	}
	if resp, _ := h.postForm("/reset/nosuchtoken", url.Values{
		"csrf_token":       {h.csrf()},
		"password":         {"brand-new-password"},
		"password_confirm": {"brand-new-password"},
	}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown token post = %d, want 400", resp.StatusCode)
	}

	// Expired token.
	token, hash := tokens.New()
	if err := h.store.CreateAuthToken(t.Context(), user.ID, store.TokenPasswordReset,
		hash, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.get("/reset/" + token); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expired token page = %d, want 400", resp.StatusCode)
	}

	// A token minted for a different purpose must not be redeemable here.
	verifyToken, verifyHash := tokens.New()
	if err := h.store.CreateAuthToken(t.Context(), user.ID, store.TokenEmailVerify,
		verifyHash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.get("/reset/" + verifyToken); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a verification token opened the reset form: %d", resp.StatusCode)
	}
}

func TestIssuingANewResetInvalidatesTheOldOne(t *testing.T) {
	h := newMailHarness(t)

	user := h.seedUser("alice")
	if err := h.store.SetEmail(t.Context(), user.ID, "alice@example.com", false); err != nil {
		t.Fatal(err)
	}

	h.get("/")
	h.postForm("/forgot", url.Values{"csrf_token": {h.csrf()}, "identifier": {"alice"}})
	first := resetTokenFrom(t, h.mailer.last(t))

	h.postForm("/forgot", url.Values{"csrf_token": {h.csrf()}, "identifier": {"alice"}})
	second := resetTokenFrom(t, h.mailer.last(t))

	if first == second {
		t.Fatal("the same token was issued twice")
	}
	if resp, _ := h.get("/reset/" + first); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("the superseded token still works: %d", resp.StatusCode)
	}
	if resp, _ := h.get("/reset/" + second); resp.StatusCode != http.StatusOK {
		t.Errorf("the newest token does not work: %d", resp.StatusCode)
	}
}

func TestResetSignsOutOtherDevices(t *testing.T) {
	h := newMailHarness(t)

	user := h.seedUser("alice")
	if err := h.store.SetEmail(t.Context(), user.ID, "alice@example.com", false); err != nil {
		t.Fatal(err)
	}
	// A device that is already signed in.
	device := h.signIn(t, user.ID)
	if resp, err := device.Get(h.server.URL + "/gallery"); err != nil {
		t.Fatal(err)
	} else if resp.Body.Close(); resp.StatusCode != http.StatusOK {
		t.Fatalf("device was not signed in to begin with: %d", resp.StatusCode)
	}

	h.get("/")
	h.postForm("/forgot", url.Values{"csrf_token": {h.csrf()}, "identifier": {"alice"}})
	token := resetTokenFrom(t, h.mailer.last(t))

	if resp, _ := h.postForm("/reset/"+token, url.Values{
		"csrf_token":       {h.csrf()},
		"password":         {"brand-new-password"},
		"password_confirm": {"brand-new-password"},
	}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset = %d, want 303", resp.StatusCode)
	}

	// The previously signed-in device must have been signed out.
	sessionResp, err := device.Get(h.server.URL + "/gallery")
	if err != nil {
		t.Fatal(err)
	}
	defer sessionResp.Body.Close()
	if sessionResp.StatusCode != http.StatusSeeOther {
		t.Errorf("the old session survived a reset: %d", sessionResp.StatusCode)
	}
}

func TestForgotWithoutMailConfigured(t *testing.T) {
	h := newHarness(t) // mail disabled
	h.get("/")

	resp, page := h.get("/forgot")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/forgot = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(page, "no mail server configured") {
		t.Error("the page does not explain that email is unavailable")
	}
	if strings.Contains(page, `action="/forgot"`) {
		t.Error("the form is offered even though it cannot do anything")
	}
}

func TestChangePasswordWhileSignedIn(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	// Wrong current password is refused.
	resp, _ := h.postForm("/settings/password", url.Values{
		"csrf_token":       {h.csrf()},
		"current_password": {"not-my-password"},
		"password":         {"brand-new-password"},
		"password_confirm": {"brand-new-password"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong current password = %d, want 403", resp.StatusCode)
	}

	// The right one works.
	resp, _ = h.postForm("/settings/password", url.Values{
		"csrf_token":       {h.csrf()},
		"current_password": {testPassword},
		"password":         {"brand-new-password"},
		"password_confirm": {"brand-new-password"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("change password = %d, want 303", resp.StatusCode)
	}

	user, err := h.store.UserByUsername(t.Context(), "marcus")
	if err != nil {
		t.Fatal(err)
	}
	if !passwordMatches(user.PasswordHash, "brand-new-password") {
		t.Error("the password was not changed")
	}
}

func TestAdminIssuesAResetLink(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	target := h.seedUser("alice")
	if err := h.store.SetEmail(t.Context(), target.ID, "alice@example.com", false); err != nil {
		t.Fatal(err)
	}

	resp, page := h.postForm("/admin/users/"+itoa64(target.ID)+"/reset", url.Values{
		"csrf_token": {h.csrf()},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("issue reset = %d, want the rendered page", resp.StatusCode)
	}
	if !strings.Contains(page, "Reset link for alice") {
		t.Errorf("the page does not name the account: %s", truncate(page))
	}

	token := extractResetLink(t, page)
	if token == "" {
		t.Fatalf("no reset link in the page: %s", truncate(page))
	}

	// The link works even though no mail was sent, which is the point of the
	// fallback.
	if resp, _ := h.get("/reset/" + token); resp.StatusCode != http.StatusOK {
		t.Errorf("the issued link does not work: %d", resp.StatusCode)
	}

	// And it is not shown again on a later page load.
	if _, again := h.get("/admin/users"); strings.Contains(again, token) {
		t.Error("the reset link is still being rendered after the fact")
	}
}

func TestAdminIssuedResetIsSingleUse(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	target := h.seedUser("alice")
	_, page := h.postForm("/admin/users/"+itoa64(target.ID)+"/reset", url.Values{
		"csrf_token": {h.csrf()},
	})
	token := extractResetLink(t, page)

	if resp, _ := h.postForm("/reset/"+token, url.Values{
		"csrf_token":       {h.csrf()},
		"password":         {"brand-new-password"},
		"password_confirm": {"brand-new-password"},
	}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset = %d, want 303", resp.StatusCode)
	}

	if resp, _ := h.get("/reset/" + token); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("the link is still usable after being spent: %d", resp.StatusCode)
	}
}

func TestEmailVerification(t *testing.T) {
	h := newMailHarness(t)
	h.get("/")

	resp, _ := h.postForm("/register", url.Values{
		"csrf_token": {h.csrf()},
		"username":   {"alice"},
		"password":   {testPassword},
		"email":      {"alice@example.com"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("register = %d, want 303", resp.StatusCode)
	}

	// Registration sends a confirmation, and the address starts unverified.
	msg := h.mailer.last(t)
	if msg.To != "alice@example.com" {
		t.Fatalf("confirmation went to %q", msg.To)
	}
	user, err := h.store.UserByUsername(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if user.EmailVerified {
		t.Error("a new address was marked verified")
	}

	if _, page := h.get("/settings/password"); !strings.Contains(page, "unconfirmed") {
		t.Error("the settings page does not show the address as unconfirmed")
	}

	// Following the link confirms it.
	token := verifyTokenFrom(t, msg)
	resp, _ = h.get("/verify/" + token)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("verify = %d, want 303", resp.StatusCode)
	}

	user, err = h.store.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !user.EmailVerified {
		t.Error("the address was not confirmed")
	}

	// A confirmation link is single use.
	if resp, _ := h.get("/verify/" + token); resp.StatusCode != http.StatusNotFound {
		t.Errorf("replaying a confirmation = %d, want 404", resp.StatusCode)
	}
}

func TestUnverifiedEmailDoesNotGateAccess(t *testing.T) {
	h := newMailHarness(t)
	h.get("/")

	h.postForm("/register", url.Values{
		"csrf_token": {h.csrf()},
		"username":   {"alice"},
		"password":   {testPassword},
		"email":      {"alice@example.com"},
	})

	// Never confirm the address: everything must still work.
	for _, path := range []string{"/gallery", "/upload", "/settings/password"} {
		resp, _ := h.get(path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s with an unverified address = %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestRegistrationSurvivesMailFailure(t *testing.T) {
	h := newMailHarness(t)
	h.mailer.failWith = errFakeMail

	h.get("/")
	resp, _ := h.postForm("/register", url.Values{
		"csrf_token": {h.csrf()},
		"username":   {"alice"},
		"password":   {testPassword},
		"email":      {"alice@example.com"},
	})
	// A broken relay must not cost somebody their account.
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("register with a failing relay = %d, want 303", resp.StatusCode)
	}
	if _, err := h.store.UserByUsername(t.Context(), "alice"); err != nil {
		t.Errorf("the account was not created: %v", err)
	}
}

func TestPasswordResetTokensAreStoredHashed(t *testing.T) {
	h := newMailHarness(t)

	user := h.seedUser("alice")
	if err := h.store.SetEmail(t.Context(), user.ID, "alice@example.com", false); err != nil {
		t.Fatal(err)
	}

	h.get("/")
	h.postForm("/forgot", url.Values{"csrf_token": {h.csrf()}, "identifier": {"alice"}})
	token := resetTokenFrom(t, h.mailer.last(t))

	// The raw token must not appear in the database anywhere.
	var stored string
	err := h.store.DB().QueryRowContext(t.Context(),
		`SELECT token_hash FROM auth_tokens WHERE user_id = ?`, user.ID).Scan(&stored)
	if err != nil {
		t.Fatalf("no token row: %v", err)
	}
	if stored == token {
		t.Error("the reset token is stored in the clear")
	}
	if stored != tokens.Hash(token) {
		t.Error("the stored value is not the digest of the token")
	}
}
