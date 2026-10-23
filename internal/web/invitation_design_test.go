// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestInvitationPageSchemaAndEscaping(t *testing.T) {
	h := newHarness(t)
	owner := h.sessionFor(t, h.provisionAdmin("boss").ID)
	resp, body := owner.get("/admin/invites")
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.StatusCode)
	}
	assertInvitationPageSchema(t, body, true)
	if !strings.Contains(body, "No invitations yet.") {
		t.Fatal("empty list has no explanation")
	}
	for _, label := range []string{"For Sam", strings.Repeat("w", 64), `<script>alert("invite")</script>`, `Quotes " & < >`} {
		resp, body = owner.post("/admin/invites", url.Values{"label": {label}, "max_uses": {"2"}, "expires_days": {"7"}})
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("invitation creation failed or may be cached", resp.StatusCode)
		}
		assertInvitationPageSchema(t, body, true)
		if !strings.Contains(body, "<h3>"+html.EscapeString(label)+"</h3>") || !strings.Contains(body, "/revoke") {
			t.Fatal("record lost its escaped label or revoke action")
		}
	}
	_, before, err := h.store.ListInvites(t.Context(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, body = owner.post("/admin/invites", url.Values{"label": {"Keep my draft"}, "max_uses": {"-1"}, "expires_days": {"7"}})
	assertInvitationPageSchema(t, body, true)
	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, `value="Keep my draft"`) {
		t.Fatal("validation lost the error or draft")
	}
	_, after, err := h.store.ListInvites(t.Context(), 100, 0)
	if err != nil || before != after {
		t.Fatal("invalid form changed invitations", err)
	}
}

func TestAccountPagesUseCompactForms(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/login", "/register"} {
		resp, body := h.get(path)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, `class="narrow account-page"`) || !strings.Contains(body, `autocomplete="username"`) {
			t.Fatalf("%s lost its compact account form", path)
		}
	}
}
