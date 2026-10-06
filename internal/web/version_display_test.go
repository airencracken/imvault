// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/config"
	"imvault/internal/models"
)

func TestVersionDisplayDefaultsPermissionsPersistenceAndEscaping(t *testing.T) {
	const stamp = "0.15.0<script>alert(1)</script>"
	h := newHarnessWith(t, func(cfg *config.Config) { cfg.Version = stamp })
	admin := h.provisionAdmin("boss")
	owner := h.sessionFor(t, admin.ID)
	member := h.sessionFor(t, h.seedUser("alex").ID)
	guest := h.newSession(t)
	_, page := guest.get("/")
	if strings.Contains(page, "0.15.0") {
		t.Fatal("version disclosed by default")
	}
	_, page = owner.get("/admin/settings")
	if !strings.Contains(page, "Running Imvault 0.15.0&lt;script&gt;") || !strings.Contains(page, `name="show_version"`) {
		t.Fatal("running version or toggle missing/unsafe")
	}
	form := url.Values{"site_name": {"Family"}, "source_url": {""}, "anonymous_ttl": {"24h"}, "default_visibility": {"members"}, "show_version": {"1"}}
	for _, actor := range []struct {
		session *session
		status  int
	}{{guest, http.StatusSeeOther}, {member, http.StatusForbidden}} {
		resp, _ := actor.session.post("/admin/settings", form)
		if resp.StatusCode != actor.status {
			t.Fatal("non-admin settings status", resp.StatusCode)
		}
	}
	branding, _, err := h.store.LoadBranding(t.Context(), models.Branding{})
	if err != nil || branding.ShowVersion {
		t.Fatal("non-admin changed version visibility", err)
	}
	resp := doForm(t, owner.client, h.server.URL, "/admin/settings", url.Values{"csrf_token": {"invalid"}, "site_name": {"Family"}, "show_version": {"1"}})
	if resp.StatusCode != 403 {
		t.Fatal("accepted missing CSRF", resp.StatusCode)
	}
	for _, enabled := range []bool{true, false, true} {
		if enabled {
			form.Set("show_version", "1")
		} else {
			form.Del("show_version")
		}
		resp, _ := owner.post("/admin/settings", form)
		if resp.StatusCode != 303 {
			t.Fatal("settings route", resp.StatusCode)
		}
		branding, _, err := h.store.LoadBranding(t.Context(), models.Branding{})
		if err != nil || branding.ShowVersion != enabled {
			t.Fatal("setting not persisted", branding, err)
		}
		// Reload exactly as startup does, without depending on the previous cache.
		h.srv.settings.Store(nil)
		if err := h.srv.reloadSettings(t.Context()); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/", "/about", "/login"} {
			_, page = guest.get(path)
			if strings.Contains(page, "Imvault 0.15.0&lt;script&gt;alert(1)&lt;/script&gt;") != enabled || strings.Contains(page, stamp) {
				t.Fatal("footer preference/escaping", path, enabled)
			}
		}
	}
	// A policy-only profile must not accidentally hide the version.
	resp, _ = owner.post("/admin/settings", url.Values{"profile": {"group"}})
	if resp.StatusCode != 303 {
		t.Fatal("profile route", resp.StatusCode)
	}
	_, page = guest.get("/")
	if !strings.Contains(page, "Imvault 0.15.0&lt;script&gt;") {
		t.Fatal("profile reset branding preference")
	}
}
