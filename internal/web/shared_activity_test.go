// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"imvault/internal/models"
)

// analyticsRows counts what the shared activity table holds. Imvault records
// nothing that names a member or a file, so it should always be zero.
func analyticsRows(t *testing.T, h *harness) int {
	t.Helper()
	var n int
	if err := h.store.AnalyticsDB().QueryRowContext(t.Context(), `SELECT count(*) FROM comfy_analytics_activity`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSignInsAndOpensRecordNothingIdentifying(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	id := uploadPlain(t, h, map[string]string{"visibility": "public"})
	for _, path := range []string{"/f/" + id, "/f/" + id, "/f/" + id + "/raw", "/f/" + id + "/thumb", "/gallery", "/f/does-not-exist", "/f/%27%3B%20DROP"} {
		h.get(path)
	}
	member := h.seedUser("alice")
	h.signIn(t, member.ID)
	if n := analyticsRows(t, h); n != 0 {
		t.Fatalf("%d analytics rows name members or files", n)
	}

	shared := httptest.NewRequest("GET", "http://example.test/auth/shared/callback", nil)
	if _, err := h.srv.sharedSignIn(httptest.NewRecorder(), shared, member.ID); err != nil {
		t.Fatal(err)
	}
	h.enableTwoFactorFor(t, member.ID)
	destination, err := h.srv.sharedSignIn(httptest.NewRecorder(), shared, member.ID)
	if err != nil || destination != "/login/2fa" {
		t.Fatal("a shared sign-in skipped the second factor", destination, err)
	}
	if n := analyticsRows(t, h); n != 0 {
		t.Fatalf("shared sign-in recorded %d identifying rows", n)
	}
}

func TestMetricsPageShowsNoPerMemberOrPerFileActivity(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	uploadPlain(t, h, map[string]string{"visibility": "public"})
	resp, body := h.get("/admin/metrics")
	if resp.StatusCode != 200 || !strings.Contains(body, "comfy-admin-dashboard") {
		t.Fatal(resp.StatusCode, truncate(body))
	}
	for _, gone := range []string{"Recent sign-ins", "comfy-activity-title", "Popular"} {
		if strings.Contains(body, gone) {
			t.Errorf("the metrics page still shows %q", gone)
		}
	}

	mod := h.seedUser("moderator")
	if err := h.store.SetUserRole(t.Context(), mod.ID, models.RoleModerator); err != nil {
		t.Fatal(err)
	}
	client := h.signIn(t, mod.ID)
	response, err := client.Get(h.server.URL + "/admin/metrics?role=admin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	mustClose(t, response.Body)
	if response.StatusCode != 403 {
		t.Fatal("a moderator opened the metrics page", response.StatusCode)
	}
}
