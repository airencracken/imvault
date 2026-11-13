// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/comfylib/autobackup"
	"github.com/airencracken/comfylib/displaytime"
)

func TestAdminSettingsCopyIsShortAndConfirmsWithoutInlineScript(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	_, page := h.get("/admin/settings")
	for _, gone := range []string{"onsubmit=", "confirm(", "deliberate grant", "rewrites the deadline", "fixed amount of disk", "most changes what the instance feels like", "three programs"} {
		if strings.Contains(page, gone) {
			t.Errorf("admin settings still contains %q", gone)
		}
	}
	for _, want := range []string{`data-confirm="Open this instance to the world?"`, `data-confirm="Clear the stored settings?"`} {
		if want == `data-confirm="Clear the stored settings?"` && !strings.Contains(page, "Stored overrides") {
			continue
		}
		if !strings.Contains(page, want) {
			t.Errorf("admin settings is missing %s", want)
		}
	}
}

func TestBackupTimesFollowTheViewersTimezone(t *testing.T) {
	h := newHarness(t)
	when := time.Date(2026, 11, 13, 23, 30, 0, 0, time.UTC)
	admin := h.provisionAdmin("boss")
	request := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	request = request.WithContext(withUser(request.Context(), admin))
	view := settingsView{
		base:    h.srv.base(request, "Instance settings"),
		Backups: autobackup.Status{Enabled: true, LastSuccess: when, NextAttempt: when.Add(time.Hour)},
		Stored:  map[string]bool{},
	}
	var page bytes.Buffer
	if err := h.srv.render.page(&page, "admin_settings", view, "Asia/Tokyo"); err != nil {
		t.Fatal(err)
	}
	body := page.String()
	if strings.Contains(body, "15:04:05 UTC") || strings.Contains(body, "2026-11-13 23:30:00 UTC") {
		t.Fatal("backup times are still formatted as UTC")
	}
	if !strings.Contains(body, displaytime.New("Asia/Tokyo").Stamp(when)) || !strings.Contains(body, "Nov 14, 2026") {
		t.Fatalf("backup time does not follow the viewer's timezone: %s", truncate(body))
	}
	if !strings.Contains(body, `datetime="2026-11-13T23:30:00Z"`) {
		t.Error("machine-readable backup time is missing or not UTC")
	}
}
