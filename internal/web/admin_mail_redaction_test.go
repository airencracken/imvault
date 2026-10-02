// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// The queue page never shows a live reset link, whether from a body or echoed
// back in a relay's error.
func TestAdminMailQueueHidesLiveLinks(t *testing.T) {
	h := newQueueHarness(t)
	admin := h.provisionAdmin("boss")
	h.seedUser("alice")
	h.mailer.failWith = errors.New("550 rejected message containing https://photos.example/reset/abcdef0123456789")

	s := h.newSession(t)
	if resp, _ := s.post("/forgot", url.Values{"identifier": {"alice"}}); resp.StatusCode != 200 {
		t.Fatalf("forgot = %d", resp.StatusCode)
	}
	_, page := h.sessionFor(t, admin.ID).get("/admin/mail")
	if strings.Contains(page, "abcdef0123456789") {
		t.Error("the queue page shows a reset token")
	}
	if !strings.Contains(page, "/reset/[redacted]") {
		t.Error("the relay error was not shown at all")
	}
	if strings.Contains(page, "open this link") {
		t.Error("the queue page shows a message body")
	}
}

func TestRedactTokenLinks(t *testing.T) {
	for in, want := range map[string]string{
		"see https://x/reset/abc_DEF-123 now": "see https://x/reset/[redacted] now",
		"/verify/xyz and /reset/q":            "/verify/[redacted] and /reset/[redacted]",
		"nothing here":                        "nothing here",
	} {
		if got := redactTokenLinks(in); got != want {
			t.Errorf("redactTokenLinks(%q) = %q, want %q", in, got, want)
		}
	}
}
