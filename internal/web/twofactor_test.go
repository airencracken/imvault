// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"imvault/internal/config"
	"imvault/internal/totp"
)

// totpCodeFor reads an account's secret the way the server would, and returns
// the code an authenticator app would be showing right now.
func (h *harness) totpCodeFor(t *testing.T, userID int64) string {
	t.Helper()

	user, err := h.store.UserByID(t.Context(), userID)
	if err != nil {
		t.Fatalf("load user: %v", err)
	}
	secret, err := h.srv.secrets.Decrypt(user.TOTPSecret)
	if err != nil {
		t.Fatalf("decrypt secret: %v", err)
	}
	code, err := totp.Code(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	return code
}

// enableTwoFactorFor enrols an account straight through the store, for tests
// that act as somebody other than the signed-in client.
func (h *harness) enableTwoFactorFor(t *testing.T, userID int64) string {
	t.Helper()

	secret, err := totp.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := h.srv.secrets.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.BeginTOTP(t.Context(), userID, encrypted); err != nil {
		t.Fatal(err)
	}
	if err := h.store.EnableTOTP(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	return secret
}

// recoveryCodesFor issues and returns recovery codes for an account, for tests
// that enrolled it through the store rather than the page.
func (h *harness) recoveryCodesFor(t *testing.T, userID int64) []string {
	t.Helper()

	codes, err := h.srv.issueRecoveryCodes(t.Context(), userID)
	if err != nil {
		t.Fatalf("issue recovery codes: %v", err)
	}
	return codes
}

// enableTwoFactor walks the enrolment flow and returns the recovery codes.
func (h *harness) enableTwoFactor(t *testing.T, userID int64) []string {
	t.Helper()

	resp, _ := h.postForm("/settings/2fa/begin", url.Values{"csrf_token": {h.csrf()}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("begin = %d, want 303", resp.StatusCode)
	}

	resp, page := h.postForm("/settings/2fa/confirm", url.Values{
		"csrf_token": {h.csrf()},
		"code":       {h.totpCodeFor(t, userID)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm = %d, want the rendered page", resp.StatusCode)
	}
	if !strings.Contains(page, "Recovery codes") {
		t.Fatalf("confirm did not show recovery codes: %s", truncate(page))
	}

	codes := recoveryCodesFrom(page)
	if len(codes) == 0 {
		t.Fatalf("no recovery codes in the response: %s", truncate(page))
	}
	return codes
}

var recoveryCodeRe = regexp.MustCompile(`<code>([A-Z0-9]{5}-[A-Z0-9]{5})</code>`)

func recoveryCodesFrom(page string) []string {
	matches := recoveryCodeRe.FindAllStringSubmatch(page, -1)
	codes := make([]string, 0, len(matches))
	for _, match := range matches {
		codes = append(codes, match[1])
	}
	return codes
}

func TestTwoFactorEnrolment(t *testing.T) {
	h := newHarness(t)
	marcus := h.registerForm("marcus")

	// Nothing is on to begin with.
	_, page := h.get("/settings/2fa")
	if !strings.Contains(page, "A password alone gets into this account") {
		t.Error("the page does not report that the second factor is off")
	}

	resp, _ := h.postForm("/settings/2fa/begin", url.Values{"csrf_token": {h.csrf()}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("begin = %d, want 303", resp.StatusCode)
	}

	// The setup page offers both a QR code and the key in a readable form, and
	// survives a reload because the secret is already stored.
	_, page = h.get("/settings/2fa")
	if !strings.Contains(page, "/settings/2fa/qr") {
		t.Error("no QR code on the setup page")
	}
	if !strings.Contains(page, "Finish setting up") {
		t.Error("the setup form is missing")
	}

	// The QR is served rather than inlined, because html/template rewrites a
	// data: URI in src to a failsafe and the image silently never appears.
	qrResp, _ := h.get("/settings/2fa/qr")
	if qrResp.StatusCode != http.StatusOK {
		t.Errorf("QR endpoint = %d, want 200", qrResp.StatusCode)
	}
	if ct := qrResp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("QR content type = %q, want image/png", ct)
	}

	user, err := h.store.UserByID(t.Context(), marcus.ID)
	if err != nil {
		t.Fatal(err)
	}
	if user.TOTPEnabled {
		t.Fatal("enrolment enabled the factor before it was confirmed")
	}
	if user.TOTPSecret == "" {
		t.Fatal("the pending secret was not stored")
	}
	// The stored secret must not be readable as-is.
	secret, err := h.srv.secrets.Decrypt(user.TOTPSecret)
	if err != nil {
		t.Fatalf("the stored secret does not decrypt: %v", err)
	}
	if strings.Contains(page, secret) {
		t.Error("the raw secret is rendered somewhere on the page")
	}

	// A wrong code must not enable it.
	resp, _ = h.postForm("/settings/2fa/confirm", url.Values{
		"csrf_token": {h.csrf()},
		"code":       {"000000"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("a wrong code returned %d, want a redirect", resp.StatusCode)
	}
	if user, _ = h.store.UserByID(t.Context(), marcus.ID); user.TOTPEnabled {
		t.Fatal("a wrong code enabled two-factor")
	}

	codes := h.enableTwoFactor(t, marcus.ID)
	if len(codes) != 10 {
		t.Errorf("got %d recovery codes, want 10", len(codes))
	}

	user, err = h.store.UserByID(t.Context(), marcus.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !user.TOTPEnabled {
		t.Error("two-factor was not enabled")
	}

	// The QR endpoint must refuse once enrolment is finished, so it cannot be
	// used to read a secret back out.
	if resp, _ := h.get("/settings/2fa/qr"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the QR endpoint answered after enrolment: %d", resp.StatusCode)
	}

	// The codes are shown once; the page afterwards must not repeat them.
	_, page = h.get("/settings/2fa")
	for _, code := range codes {
		if strings.Contains(page, code) {
			t.Errorf("recovery code %s is still being rendered", code)
		}
	}
	if !strings.Contains(page, "10 recovery codes left") {
		t.Errorf("the page does not report the remaining codes: %s", truncate(page))
	}
}

func TestSignInWithTwoFactor(t *testing.T) {
	h := newHarness(t)
	marcus := h.registerForm("marcus")
	h.enableTwoFactor(t, marcus.ID)

	// A browser with no session of its own.
	visitor := h.newSession(t)
	if visitor.signedIn() {
		t.Fatal("a fresh session is already signed in")
	}

	resp, _ := visitor.post("/login", url.Values{
		"username": {"marcus"},
		"password": {testPassword},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login = %d, want 303", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); location != "/login/2fa" {
		t.Fatalf("redirected to %q, want /login/2fa", location)
	}
	if visitor.signedIn() {
		t.Fatal("the password alone granted a session")
	}

	// A wrong code does not sign in.
	resp, page := visitor.post("/login/2fa", url.Values{"code": {"000000"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a wrong code returned %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(page, "did not match") {
		t.Errorf("the failure is not explained: %s", truncate(page))
	}
	if visitor.signedIn() {
		t.Fatal("a wrong code granted a session")
	}

	// The right code does. A wrong attempt does not spend the pending token, so
	// this works without starting over.
	resp, _ = visitor.post("/login/2fa", url.Values{"code": {h.totpCodeFor(t, marcus.ID)}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the correct code returned %d, want 303", resp.StatusCode)
	}
	if !visitor.signedIn() {
		t.Error("the correct code did not grant a session")
	}
}

func TestTwoFactorCodesCannotBeReplayed(t *testing.T) {
	h := newHarness(t)
	marcus := h.registerForm("marcus")
	h.enableTwoFactor(t, marcus.ID)

	code := h.totpCodeFor(t, marcus.ID)

	// First sign-in with this code succeeds.
	visitor := h.newSession(t)
	visitor.signInTo("marcus", testPassword, code)
	if !visitor.signedIn() {
		t.Fatal("the first use of the code did not sign in")
	}

	// A second browser tries the same code inside its window.
	replay := h.newSession(t)
	resp, page := replay.signInTo("marcus", testPassword, code)
	if resp.StatusCode == http.StatusSeeOther {
		t.Fatal("the same code was accepted twice")
	}
	if !strings.Contains(page, "already been used") {
		t.Errorf("the replay is not explained: %s", truncate(page))
	}
	if replay.signedIn() {
		t.Error("a replayed code granted a session")
	}
}

func TestRecoveryCodeSignsInOnce(t *testing.T) {
	h := newHarness(t)
	marcus := h.registerForm("marcus")
	codes := h.enableTwoFactor(t, marcus.ID)

	visitor := h.newSession(t)
	resp, _ := visitor.signInTo("marcus", testPassword, codes[0])
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("a recovery code returned %d, want 303", resp.StatusCode)
	}
	if !visitor.signedIn() {
		t.Fatal("a recovery code did not sign in")
	}

	remaining, err := h.store.RecoveryCodesRemaining(t.Context(), marcus.ID)
	if err != nil {
		t.Fatal(err)
	}
	if remaining != len(codes)-1 {
		t.Errorf("%d codes remain, want %d", remaining, len(codes)-1)
	}

	// The same code will not work again.
	again := h.newSession(t)
	resp, _ = again.signInTo("marcus", testPassword, codes[0])
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("a spent recovery code was accepted again")
	}
	if again.signedIn() {
		t.Error("a spent recovery code granted a session")
	}
}

func TestSecondFactorPromptNeedsACompletedPasswordStep(t *testing.T) {
	h := newHarness(t)
	marcus := h.registerForm("marcus")
	h.enableTwoFactor(t, marcus.ID)

	// Without the pending cookie there is nothing to prove, so the prompt sends
	// the caller back to the start.
	visitor := h.newSession(t)

	resp, _ := visitor.get("/login/2fa")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("/login/2fa without a pending token = %d, want 303", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); location != "/login" {
		t.Errorf("redirected to %q, want /login", location)
	}

	// Nor can a valid code be posted straight in. The answer is a redirect back
	// to the start, not a session.
	resp, _ = visitor.post("/login/2fa", url.Values{"code": {h.totpCodeFor(t, marcus.ID)}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("posting a code without the password step = %d, want 303", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); location != "/login" {
		t.Errorf("redirected to %q, want /login", location)
	}
	if visitor.signedIn() {
		t.Error("posting a code without the password step signed somebody in")
	}
}

func TestDisablingTwoFactorNeedsBothFactors(t *testing.T) {
	h := newHarness(t)
	marcus := h.registerForm("marcus")
	codes := h.enableTwoFactor(t, marcus.ID)

	// Password alone is not enough.
	h.postForm("/settings/2fa/disable", url.Values{
		"csrf_token": {h.csrf()},
		"password":   {testPassword},
	})
	if user, _ := h.store.UserByID(t.Context(), marcus.ID); !user.TOTPEnabled {
		t.Fatal("the password alone turned two-factor off")
	}

	// Wrong password with a good code is not enough either.
	h.postForm("/settings/2fa/disable", url.Values{
		"csrf_token": {h.csrf()},
		"password":   {"not-the-password"},
		"code":       {codes[0]},
	})
	if user, _ := h.store.UserByID(t.Context(), marcus.ID); !user.TOTPEnabled {
		t.Fatal("a wrong password turned two-factor off")
	}

	// Both together are.
	resp, _ := h.postForm("/settings/2fa/disable", url.Values{
		"csrf_token": {h.csrf()},
		"password":   {testPassword},
		"code":       {codes[1]},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("disable = %d, want 303", resp.StatusCode)
	}

	user, err := h.store.UserByID(t.Context(), marcus.ID)
	if err != nil {
		t.Fatal(err)
	}
	if user.TOTPEnabled || user.TOTPSecret != "" {
		t.Error("the second factor was not cleared")
	}

	// The recovery codes go with it, or they would still open the account.
	remaining, err := h.store.RecoveryCodesRemaining(t.Context(), marcus.ID)
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("%d recovery codes survived disabling two-factor", remaining)
	}
}

func TestRegeneratingRecoveryCodesInvalidatesTheOldSet(t *testing.T) {
	h := newHarness(t)
	marcus := h.registerForm("marcus")
	old := h.enableTwoFactor(t, marcus.ID)

	resp, page := h.postForm("/settings/2fa/recovery", url.Values{
		"csrf_token": {h.csrf()},
		"password":   {testPassword},
		"code":       {h.totpCodeFor(t, marcus.ID)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("regenerate = %d, want the rendered page", resp.StatusCode)
	}

	fresh := recoveryCodesFrom(page)
	if len(fresh) == 0 {
		t.Fatal("no new codes were shown")
	}
	for _, code := range fresh {
		for _, previous := range old {
			if code == previous {
				t.Error("a regenerated set reused an old code")
			}
		}
	}

	// An old code is no longer accepted.
	h.postForm("/logout", url.Values{"csrf_token": {h.csrf()}})
	h.postForm("/login", url.Values{
		"csrf_token": {h.csrf()}, "username": {"marcus"}, "password": {testPassword},
	})
	resp, _ = h.postForm("/login/2fa", url.Values{"csrf_token": {h.csrf()}, "code": {old[0]}})
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("a recovery code from the replaced set still works")
	}
}

func TestAdministratorCanClearTwoFactor(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	alice := h.seedUser("alice")
	client := h.signIn(t, alice.ID)
	_ = client

	// Give alice a second factor by enrolling on her behalf through the store,
	// as only the flow above needs a browser.
	h.enableTwoFactorFor(t, alice.ID)

	// An administrator sees it and can clear it.
	_, page := h.get("/admin/users")
	if !strings.Contains(page, "Clear 2FA") {
		t.Fatalf("no way to clear a second factor: %s", truncate(page))
	}

	resp, body := h.postHTMX("/admin/users/"+itoa64(alice.ID)+"/2fa", url.Values{
		"csrf_token": {h.csrf()},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear 2FA = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Cleared two-factor authentication") {
		t.Errorf("the notice does not report it: %s", truncate(body))
	}

	user, err := h.store.UserByID(t.Context(), alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if user.TOTPEnabled {
		t.Error("two-factor was not cleared")
	}
}

func TestSignInRateLimiting(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.LoginRatePerHour = 60
		cfg.LoginBurst = 3
	})
	h.get("/")

	var last int
	for i := 0; i < 4; i++ {
		resp, _ := h.postForm("/login", url.Values{
			"csrf_token": {h.csrf()},
			"username":   {"marcus"},
			"password":   {"wrong"},
		})
		last = resp.StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("the fourth attempt returned %d, want 429", last)
	}
}
