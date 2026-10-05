// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"imvault/internal/config"
	"imvault/internal/models"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPublicHouseRulesAndInvitationEntrance(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.AllowSignup = false
		cfg.InviteOnly = true
		cfg.AllowAnonymousUploads = false
	})
	admin := h.provisionAdmin("keeper")
	member := h.seedUser("relative")
	session := h.sessionFor(t, admin.ID)
	resp, body := session.post("/admin/settings", url.Values{"site_name": {"Our album"}, "house_rules": {"<script>ask first</script>"}, "owner_contact": {"Ask keeper here"}, "anonymous_ttl": {"1h"}, "default_visibility": {string(models.VisibilityMembers)}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("settings: %d %s", resp.StatusCode, body)
	}
	guest := h.newSession(t)
	for _, path := range []string{"/", "/login"} {
		resp, page := guest.get(path)
		if resp.StatusCode != 200 || !strings.Contains(page, "Have an invitation?") || !strings.Contains(page, `href="/about"`) {
			t.Fatalf("invitation entrance missing on %s: %d", path, resp.StatusCode)
		}
	}
	resp, page := guest.get("/about")
	for _, want := range []string{"keeper", "Ask keeper here", "&lt;script&gt;ask first&lt;/script&gt;", "including private ones"} {
		if !strings.Contains(page, want) {
			t.Errorf("about missing %q: %s", want, page)
		}
	}
	if resp.StatusCode != 200 || strings.Contains(page, member.Username) || strings.Contains(page, "<script>ask first") {
		t.Fatal("public about leaks member list or renders markup")
	}
	memberSession := h.sessionFor(t, member.ID)
	resp, _ = memberSession.post("/admin/settings", url.Values{"house_rules": {"changed"}})
	if resp.StatusCode != 403 {
		t.Fatalf("member changed house rules: %d", resp.StatusCode)
	}
}
