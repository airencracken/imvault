// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The page somebody was headed for before signing in is where the second step
// sends them, and an off-site target is still refused at both steps.
func TestNextSurvivesTheSecondFactor(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")
	marcus, err := h.store.UserByUsername(t.Context(), "marcus")
	if err != nil {
		t.Fatal(err)
	}
	h.enableTwoFactor(t, marcus.ID)

	for _, tc := range []struct{ next, want string }{
		{"/albums?page=2", "/albums?page=2"},
		{`/\evil.example`, "/gallery"},
	} {
		visitor := h.newSession(t)
		resp, _ := visitor.post("/login", url.Values{"username": {"marcus"}, "password": {testPassword}, "next": {tc.next}})
		location := resp.Header.Get("Location")
		if !strings.HasPrefix(location, "/login/2fa") {
			t.Fatalf("password step went to %q", location)
		}
		_, page := visitor.get(location)
		if tc.want != "/gallery" && !strings.Contains(page, `name="next"`) {
			t.Error("the code prompt dropped the destination")
		}
		resp, _ = visitor.post("/login/2fa", url.Values{"code": {h.totpCodeFor(t, marcus.ID)}, "next": {tc.next}})
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != tc.want {
			t.Errorf("next=%q: code step = %d -> %q, want %q", tc.next, resp.StatusCode, resp.Header.Get("Location"), tc.want)
		}
		// A code is spent once; clear the step so the next round can sign in.
		if _, err := h.store.DB().ExecContext(t.Context(), `UPDATE users SET totp_last_step = 0 WHERE id = ?`, marcus.ID); err != nil {
			t.Fatal(err)
		}
	}
}
