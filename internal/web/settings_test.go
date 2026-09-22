// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"imvault/internal/config"
	"imvault/internal/models"
)

func TestInstanceSettingsOverrideTheEnvironment(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.AllowSignup = true
		cfg.AllowAnonymousUploads = true
		cfg.AnonymousTTL = 24 * time.Hour
	})
	h.provisionAdmin("boss")

	// Before anything is stored, the configuration is in charge.
	policy := h.srv.policy()
	if !policy.AllowSignup || !policy.AllowAnonymousUploads || policy.AnonymousTTL != 24*time.Hour {
		t.Fatalf("the policy does not match the configuration: %+v", policy)
	}

	resp, _ := h.postForm("/admin/settings", url.Values{
		"csrf_token":              {h.csrf()},
		"anonymous_ttl":           {"6h"},
		"allow_signup":            {"0"},
		"allow_anonymous_uploads": {"0"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save = %d, want 303", resp.StatusCode)
	}

	policy = h.srv.policy()
	if policy.AllowSignup {
		t.Error("signup is still open")
	}
	if policy.AllowAnonymousUploads {
		t.Error("anonymous uploads are still on")
	}
	if policy.AnonymousTTL != 6*time.Hour {
		t.Errorf("retention = %s, want 6h", policy.AnonymousTTL)
	}

	// And the stored values survive a restart, which is what makes them
	// settings rather than a cache.
	reloaded, _, err := h.store.LoadSettings(t.Context(), models.Settings{
		AllowSignup:           true,
		AllowAnonymousUploads: true,
		AnonymousTTL:          24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.AllowSignup || reloaded.AllowAnonymousUploads || reloaded.AnonymousTTL != 6*time.Hour {
		t.Errorf("stored settings did not survive a reload: %+v", reloaded)
	}
}

func TestConfiguredAdmissionAndStorageLimitsApply(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.InviteOnly = true
		cfg.MaxTotalBytes = 1
	})
	h.provisionAdmin("boss")
	resp, _ := h.postForm("/register", url.Values{
		"csrf_token": {h.csrf()}, "username": {"uninvited"}, "password": {testPassword},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("registration without invitation = %d, want 403", resp.StatusCode)
	}
	_, body := h.uploadFiles(map[string]string{}, []uploadFile{{name: "photo.png", data: pngFixture(t, 16, 16)}})
	if countStoredFiles(t, h) != 0 || !strings.Contains(body, "instance is full") {
		t.Fatalf("configured storage ceiling was ignored: %s", truncate(body))
	}
}

func TestAdminCanSwitchAnonymousUploadsOffAndOn(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	anon := h.newSession(t)
	for _, enabled := range []bool{false, true} {
		form := url.Values{"csrf_token": {h.csrf()}, "anonymous_ttl": {"1h"},
			"default_visibility": {"members"}}
		if enabled {
			form.Set("allow_anonymous_uploads", "1")
		}
		resp, _ := h.postForm("/admin/settings", form)
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("save switch = %d", resp.StatusCode)
		}
		// Reload from persistent storage, as startup does.
		if err := h.srv.installSettings(t.Context()); err != nil {
			t.Fatal(err)
		}
		_, page := h.get("/admin/settings")
		checked := strings.Contains(page, `name="allow_anonymous_uploads" value="1" checked`)
		if checked != enabled {
			t.Errorf("switch checked=%t, want %t", checked, enabled)
		}
		resp, _ = anon.upload(map[string]string{}, []uploadFile{{name: "anon.png", data: pngFixture(t, 16, 16)}})
		want := http.StatusForbidden
		if enabled {
			want = http.StatusOK
		}
		if resp.StatusCode != want {
			t.Errorf("anonymous upload with enabled=%t = %d, want %d", enabled, resp.StatusCode, want)
		}
	}
}

func TestStoredSettingsActuallyGovern(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.AllowSignup = true
		cfg.AllowAnonymousUploads = true
		cfg.AnonymousTTL = 24 * time.Hour
	})
	h.provisionAdmin("boss")

	// Close both from the admin page.
	h.postForm("/admin/settings", url.Values{
		"csrf_token":              {h.csrf()},
		"anonymous_ttl":           {"1h"},
		"allow_signup":            {"0"},
		"allow_anonymous_uploads": {"0"},
	})

	// Registration is refused even though the configuration permits it.
	h.get("/")
	resp, _ := h.postForm("/register", url.Values{
		"csrf_token": {h.csrf()},
		"username":   {"newcomer"},
		"password":   {testPassword},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("registration = %d, want 403 with signup closed", resp.StatusCode)
	}
	if _, err := h.store.UserByUsername(t.Context(), "newcomer"); err == nil {
		t.Error("an account was created with signup closed")
	}

	// Anonymous uploads are refused, and the uploader redirects them away.
	anon := h.newSession(t)
	anonResp, _ := anon.get("/upload")
	if anonResp.StatusCode != http.StatusSeeOther {
		t.Errorf("anonymous upload page = %d, want a redirect to sign in", anonResp.StatusCode)
	}

	uploadResp, _ := anon.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "anon.png", data: pngFixture(t, 32, 32)},
	})
	if uploadResp.StatusCode != http.StatusForbidden {
		t.Errorf("anonymous upload = %d, want 403", uploadResp.StatusCode)
	}

	// The navigation stops offering either.
	_, page := h.get("/")
	if strings.Contains(page, `href="/register"`) {
		t.Error("the navigation still offers registration")
	}
}

func TestRetentionWindowAppliesToExistingUploads(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.AllowAnonymousUploads = true
		cfg.AnonymousTTL = 30 * 24 * time.Hour
	})
	h.provisionAdmin("boss")

	// Two anonymous uploads, with a month to live.
	for _, name := range []string{"one.png", "two.png"} {
		anon := h.newSession(t)
		resp, body := anon.upload(map[string]string{"public": "1"}, []uploadFile{
			{name: name, data: pngFixture(t, 32, 32)},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("anonymous upload = %d", resp.StatusCode)
		}

		id := firstFileID(t, body)
		file, err := h.store.FileByID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if file.ExpiresAt == nil {
			t.Fatal("an anonymous upload was stored without a deadline")
		}
		if time.Until(*file.ExpiresAt) < 29*24*time.Hour {
			t.Errorf("deadline is only %s away, want a month", time.Until(*file.ExpiresAt))
		}
	}

	// Shorten the window, which must reach backwards: otherwise a response to
	// abuse would not take effect until the old deadlines passed.
	resp, _ := h.postForm("/admin/settings", url.Values{
		"csrf_token":              {h.csrf()},
		"anonymous_ttl":           {"1h"},
		"allow_signup":            {"1"},
		"allow_anonymous_uploads": {"1"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save = %d, want 303", resp.StatusCode)
	}

	files, err := h.store.ListFiles(t.Context(), fileQueryAnonymous())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("found %d anonymous uploads, want 2", len(files))
	}

	for _, file := range files {
		if file.ExpiresAt == nil {
			t.Errorf("file %s lost its deadline", file.ID)
			continue
		}
		remaining := time.Until(*file.ExpiresAt)
		if remaining > time.Hour+time.Minute {
			t.Errorf("file %s still has %s left, want about an hour", file.ID, remaining)
		}
	}
}

func TestShorteningTheWindowMakesOldUploadsCollectable(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.AllowAnonymousUploads = true
		cfg.AnonymousTTL = 30 * 24 * time.Hour
	})
	h.provisionAdmin("boss")

	anon := h.newSession(t)
	resp, body := anon.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "old.png", data: pngFixture(t, 32, 32)},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("upload = %d", resp.StatusCode)
	}
	id := firstFileID(t, body)

	// Age it past the window we are about to set. The deadline is measured from
	// when the upload arrived, so an old upload has none left.
	old := time.Now().Add(-48 * time.Hour).UTC().Unix()
	if _, err := h.store.DB().ExecContext(t.Context(),
		`UPDATE files SET created_at = ? WHERE id = ?`, old, id); err != nil {
		t.Fatal(err)
	}

	h.postForm("/admin/settings", url.Values{
		"csrf_token":              {h.csrf()},
		"anonymous_ttl":           {"1h"},
		"allow_signup":            {"1"},
		"allow_anonymous_uploads": {"1"},
	})

	// The reaper's next pass should take it.
	h.srv.runCleanup(t.Context())

	if _, err := h.store.FileByID(t.Context(), id); err == nil {
		t.Error("an upload older than the shortened window survived the reaper")
	}
}

func TestClearingSettingsRestoresTheConfiguration(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.AllowSignup = false
		cfg.AllowAnonymousUploads = true
		cfg.AnonymousTTL = 12 * time.Hour
	})
	h.provisionAdmin("boss")

	h.postForm("/admin/settings", url.Values{
		"csrf_token":              {h.csrf()},
		"anonymous_ttl":           {"6h"},
		"allow_signup":            {"1"},
		"allow_anonymous_uploads": {"0"},
	})
	if !h.srv.policy().AllowSignup {
		t.Fatal("the override did not take")
	}

	resp, _ := h.postForm("/admin/settings/clear", url.Values{"csrf_token": {h.csrf()}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("clear = %d, want 303", resp.StatusCode)
	}

	policy := h.srv.policy()
	if policy.AllowSignup {
		t.Error("signup should be closed again, as the configuration says")
	}
	if !policy.AllowAnonymousUploads {
		t.Error("anonymous uploads should be open again")
	}
	if policy.AnonymousTTL != 12*time.Hour {
		t.Errorf("retention = %s, want the configured 12h", policy.AnonymousTTL)
	}
}

func TestRetentionInputAcceptsWhatPeopleType(t *testing.T) {
	tests := map[string]time.Duration{
		"24":    24 * time.Hour,
		"1":     time.Hour,
		"24h":   24 * time.Hour,
		"90m":   90 * time.Minute,
		"7d":    7 * 24 * time.Hour,
		" 48 ":  48 * time.Hour,
		"30m":   30 * time.Minute,
		"720h":  720 * time.Hour,
		"2d":    48 * time.Hour,
		"1h30m": 90 * time.Minute,
	}

	for in, want := range tests {
		got, err := parseRetention(in)
		if err != nil {
			t.Errorf("parseRetention(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseRetention(%q) = %s, want %s", in, got, want)
		}
	}

	for _, in := range []string{"", "   ", "0", "-1", "0h", "soon", "1x"} {
		if _, err := parseRetention(in); err == nil {
			t.Errorf("parseRetention(%q) was accepted", in)
		}
	}
}

func TestRetentionRoundTripsThroughTheForm(t *testing.T) {
	// The value shown in the box must parse back to what it came from, or
	// saving without editing would change it.
	for _, window := range []time.Duration{
		24 * time.Hour, time.Hour, 30 * time.Minute, 7 * 24 * time.Hour, 90 * time.Minute,
	} {
		formatted := formatRetention(window)
		parsed, err := parseRetention(formatted)
		if err != nil {
			t.Errorf("formatRetention(%s) produced %q, which does not parse: %v", window, formatted, err)
			continue
		}
		if parsed != window {
			t.Errorf("round trip of %s via %q gave %s", window, formatted, parsed)
		}
	}
}

func TestOnlyAnAdministratorCanChangeSettings(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	alice := h.seedUser("alice")
	client := h.signIn(t, alice.ID)
	token := h.csrfFor(t, client)

	// The page is refused.
	resp, err := client.Get(h.server.URL + "/admin/settings")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin GET = %d, want 403", resp.StatusCode)
	}

	// And so is the write, with a token that is otherwise valid.
	write := doForm(t, client, h.server.URL, "/admin/settings", url.Values{
		"csrf_token":    {token},
		"anonymous_ttl": {"9999"},
	})
	if write.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin POST = %d, want 403", write.StatusCode)
	}

	if h.srv.policy().AnonymousTTL != h.srv.cfg.AnonymousTTL {
		t.Error("a non-administrator changed the retention window")
	}
}
