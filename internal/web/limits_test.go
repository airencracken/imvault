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

func TestUploadsStopAtTheInstanceCeiling(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	// With no ceiling the first upload lands.
	resp, body := h.uploadFiles(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "first.png", data: pngFixture(t, 64, 64)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d, want 200", resp.StatusCode)
	}
	if firstFileID(t, body) == "" {
		t.Fatal("the first upload produced no file")
	}

	total, err := h.store.TotalStoredBytes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 {
		t.Fatal("nothing was stored, so the ceiling cannot be tested")
	}

	// Set the ceiling to exactly what is there, so any more is too much.
	h.setPolicy(t, models.Settings{
		AllowAnonymousUploads: true,
		DefaultVisibility:     models.VisibilityMembers,
		MaxTotalBytes:         total,
	})

	resp, body = h.uploadFiles(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "second.png", data: pngFixture(t, 64, 64)},
	})
	// The uploader answers with the fragment either way, so the status is not
	// the signal here; the message is.
	if !strings.Contains(body, "instance is full") {
		t.Errorf("the refusal does not say the instance is full: %s", truncate(body))
	}
	if count := countStoredFiles(t, h); count != 1 {
		t.Errorf("%d files stored, want 1", count)
	}
}

func TestAnonymousUploadsCountTowardTheInstanceCeiling(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	// One anonymous upload, which no account's quota covers.
	anon := h.newSession(t)
	resp, _ := anon.upload(map[string]string{"visibility": "public"}, []uploadFile{
		{name: "anon.png", data: pngFixture(t, 64, 64)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous upload = %d", resp.StatusCode)
	}

	total, err := h.store.TotalStoredBytes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h.setPolicy(t, models.Settings{
		AllowAnonymousUploads: true,
		DefaultVisibility:     models.VisibilityMembers,
		MaxTotalBytes:         total,
	})

	// A signed-in upload is still refused, because the anonymous one is part of
	// the total. A ceiling that ignored them would be no ceiling on a public
	// instance.
	_, body := h.uploadFiles(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "second.png", data: pngFixture(t, 64, 64)},
	})
	if !strings.Contains(body, "instance is full") {
		t.Errorf("an anonymous upload did not count toward the ceiling: %s", truncate(body))
	}
}

func TestTheInstanceCeilingIsAnInstanceSetting(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	if h.srv.policy().MaxTotalBytes != 0 {
		t.Fatal("the harness starts with a ceiling")
	}

	resp, _ := h.postForm("/admin/settings", url.Values{
		"csrf_token":         {h.csrf()},
		"allow_signup":       {"1"},
		"anonymous_ttl":      {"24h"},
		"default_visibility": {"members"},
		"max_total_mb":       {"64"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save settings = %d, want 303", resp.StatusCode)
	}
	if got := h.srv.policy().MaxTotalBytes; got != 64<<20 {
		t.Errorf("ceiling = %d, want %d", got, 64<<20)
	}

	// The page shows it back in the unit it accepts.
	_, page := h.get("/admin/settings")
	if !strings.Contains(page, `name="max_total_mb"`) || !strings.Contains(page, `value="64"`) {
		t.Error("the settings page does not show the saved ceiling")
	}

	// A nonsense value is refused rather than silently clearing the ceiling.
	resp, _ = h.postForm("/admin/settings", url.Values{
		"csrf_token":         {h.csrf()},
		"anonymous_ttl":      {"24h"},
		"default_visibility": {"members"},
		"max_total_mb":       {"lots"},
	})
	if got := h.srv.policy().MaxTotalBytes; got != 64<<20 {
		t.Errorf("a bad value changed the ceiling to %d", got)
	}

	// Blank means no ceiling, which is a real choice rather than an error.
	resp, _ = h.postForm("/admin/settings", url.Values{
		"csrf_token":         {h.csrf()},
		"allow_signup":       {"1"},
		"anonymous_ttl":      {"24h"},
		"default_visibility": {"members"},
		"max_total_mb":       {""},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save settings = %d", resp.StatusCode)
	}
	if got := h.srv.policy().MaxTotalBytes; got != 0 {
		t.Errorf("ceiling = %d, want none", got)
	}
}

func TestConcurrentUploadsAreBounded(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.MaxConcurrentUploads = 1
	})
	h.registerForm("boss")

	// Shorten the queue so the test does not sit for the real ten seconds.
	h.srv.processing.wait = 50 * time.Millisecond

	// Hold the only slot, as a slow upload would.
	release, ok := h.srv.processing.acquire(t.Context())
	if !ok {
		t.Fatal("could not take the only slot")
	}

	resp, body := h.uploadFiles(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "busy.png", data: pngFixture(t, 32, 32)},
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("upload while every slot is taken = %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Error("the refusal carries no Retry-After")
	}
	if !strings.Contains(body, "busy with other uploads") {
		t.Errorf("the refusal does not explain itself: %s", truncate(body))
	}

	// Freeing the slot lets the same upload through, so the bound is a queue
	// rather than a wall.
	release()

	resp, body = h.uploadFiles(map[string]string{"visibility": "members"}, []uploadFile{
		{name: "busy.png", data: pngFixture(t, 32, 32)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload after the slot freed = %d, want 200 (%s)", resp.StatusCode, truncate(body))
	}
	if firstFileID(t, body) == "" {
		t.Error("the upload did not produce a file")
	}
}

func TestTheAPIRefusesOverloadAsJSON(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.MaxConcurrentUploads = 1
	})
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	h.srv.processing.wait = 50 * time.Millisecond
	release, ok := h.srv.processing.acquire(t.Context())
	if !ok {
		t.Fatal("could not take the only slot")
	}
	defer release()

	resp, raw := h.apiUpload(key, map[string]string{"visibility": "members"}, map[string][]byte{
		"busy.png": pngFixture(t, 32, 32),
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("api upload while full = %d, want 503", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("the API refusal carries no Retry-After")
	}
	// A script needs a status it can act on, not a fragment it cannot parse.
	if !strings.Contains(string(raw), "busy") {
		t.Errorf("the API refusal is not JSON about being busy: %s", truncate(string(raw)))
	}
}

// countStoredFiles is how many files the instance holds, whatever their owner.
func countStoredFiles(t *testing.T, h *harness) int {
	t.Helper()

	var n int
	if err := h.store.DB().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM files`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
