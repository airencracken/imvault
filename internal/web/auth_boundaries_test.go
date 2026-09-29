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

func assertSecondFactorPrompt(t *testing.T, browser *session) {
	t.Helper()
	resp, body := browser.get("/login/2fa")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Finish signing in as") || strings.Contains(body, "Password accepted") {
		t.Fatalf("second-factor prompt misrepresents the sign-in method: status=%d body=%s", resp.StatusCode, body)
	}
}

func TestResetRequiresEnabledSecondFactor(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	h.enableTwoFactorFor(t, u.ID)
	token, hash := tokens.New()
	if err := h.store.CreateAuthToken(t.Context(), u.ID, store.TokenPasswordReset, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	browser := h.newSession(t)
	resp, _ := browser.post("/reset/"+token, url.Values{"password": {"replacement-password"}, "password_confirm": {"replacement-password"}})
	if resp.StatusCode >= 500 {
		t.Fatalf("reset failed for an unrelated reason: %d", resp.StatusCode)
	}
	if browser.signedIn() {
		t.Fatal("reset link granted an authenticated gallery session without a TOTP or recovery code")
	}
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login/2fa" {
		t.Fatalf("reset must continue to the second factor: %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	assertSecondFactorPrompt(t, browser)
	resp, _ = browser.post("/login/2fa", url.Values{"code": {h.totpCodeFor(t, u.ID)}})
	if resp.StatusCode != http.StatusSeeOther || !browser.signedIn() {
		t.Fatal("correct second factor did not complete password recovery")
	}
}

func TestOIDCRequiresEnabledSecondFactor(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "email_match", true: "linked_subject"}[linked], func(t *testing.T) {
			idp := newFakeIDP(t)
			h := oidcHarness(t, idp)
			u := h.seedUser("alice")
			h.enableTwoFactorFor(t, u.ID)
			if err := h.store.SetEmail(t.Context(), u.ID, "alice@example.com", true); err != nil {
				t.Fatal(err)
			}
			if linked {
				if _, err := h.store.LinkIdentity(t.Context(), u.ID, idp.server.URL, "subject-audit", "alice@example.com"); err != nil {
					t.Fatal(err)
				}
			}
			idp.setIdentity("subject-audit", "alice@example.com", true, "alice")
			browser := h.newSession(t)
			resp := h.callback(t, idp, browser.client, h.authorize(t, browser.client, ""))
			if resp.StatusCode >= 500 {
				t.Fatalf("OIDC failed for an unrelated reason: %d", resp.StatusCode)
			}
			if browser.signedIn() {
				t.Fatal("OIDC granted a gallery session without the account's enabled second factor")
			}
			if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login/2fa" {
				t.Fatalf("OIDC must continue to the second factor: %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
			}
			assertSecondFactorPrompt(t, browser)
			resp, _ = browser.post("/login/2fa", url.Values{"code": {h.totpCodeFor(t, u.ID)}})
			if resp.StatusCode != http.StatusSeeOther || !browser.signedIn() {
				t.Fatal("correct second factor did not complete OIDC sign-in")
			}
		})
	}
}

func TestPasswordChangeRevokesOutstandingReset(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	if err := h.store.SetPassword(t.Context(), u.ID, mustHashPassword(t, "original-password")); err != nil {
		t.Fatal(err)
	}
	token, hash := tokens.New()
	if err := h.store.CreateAuthToken(t.Context(), u.ID, store.TokenPasswordReset, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	owner := h.sessionFor(t, u.ID)
	resp, _ := owner.post("/settings/password", url.Values{"current_password": {"original-password"}, "password": {"replacement-password"}, "password_confirm": {"replacement-password"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("password change: %d", resp.StatusCode)
	}
	attacker := h.newSession(t)
	resp, _ = attacker.post("/reset/"+token, url.Values{"password": {"attacker-password"}, "password_confirm": {"attacker-password"}})
	if attacker.signedIn() {
		t.Fatal("a reset link issued before the password change still takes over the account")
	}
	updated, err := h.store.UserByID(t.Context(), u.ID)
	if err != nil || !passwordMatches(updated.PasswordHash, "replacement-password") {
		t.Fatalf("old reset link replaced the new password: status=%d err=%v", resp.StatusCode, err)
	}
}

func TestFailedPasswordChangeIsAtomic(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	if err := h.store.SetPassword(t.Context(), u.ID, mustHashPassword(t, "original-password")); err != nil {
		t.Fatal(err)
	}
	owner := h.sessionFor(t, u.ID)
	if _, err := h.store.DB().ExecContext(t.Context(), `CREATE TRIGGER audit_reject_session_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'audit session deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	resp, _ := owner.post("/settings/password", url.Values{"current_password": {"original-password"}, "password": {"replacement-password"}, "password_confirm": {"replacement-password"}})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("injected failure did not occur: %d", resp.StatusCode)
	}
	updated, err := h.store.UserByID(t.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !passwordMatches(updated.PasswordHash, "original-password") {
		t.Fatal("failed password change committed the new password while leaving old sessions valid")
	}
}
