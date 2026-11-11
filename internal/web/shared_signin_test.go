// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharedSignInPreservesLocalSecondFactorAndDisabledAccounts(t *testing.T) {
	h := newHarnessTransport(t, nil, mailOff, false)
	user := h.seedUser("shared-member")
	h.enableTwoFactorFor(t, user.ID)
	r := httptest.NewRequest("GET", "http://example.org/auth/shared/callback", nil)
	w := httptest.NewRecorder()
	destination, err := h.srv.sharedSignIn(w, r, user.ID)
	if err != nil || destination != "/login/2fa" {
		t.Fatal(destination, err)
	}
	pending := false
	for _, c := range w.Result().Cookies() {
		if strings.Contains(c.Name, "pending") {
			pending = true
		}
		if c.Name == sessionCookie && c.Value != "" {
			t.Fatal("second factor bypassed")
		}
	}
	if !pending {
		t.Fatal("missing pending second-factor session")
	}
	if _, err = h.store.DB().Exec("UPDATE users SET disabled=1 WHERE id=?", user.ID); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	if _, err = h.srv.sharedSignIn(w, r, user.ID); err == nil {
		t.Fatal("disabled account signed in")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("disabled account received a session")
	}
}
