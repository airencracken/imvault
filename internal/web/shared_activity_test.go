// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"io"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"imvault/internal/models"
)

// siteHealthRows returns every stored route label and the security totals,
// after writing the monitor's in-memory counts.
func siteHealthRows(t *testing.T, h *harness) (routes []string, failed, limited, rejected int) {
	t.Helper()
	h.srv.waitBackground()
	if err := h.srv.FlushMetrics(t.Context()); err != nil {
		t.Fatal(err)
	}
	db := h.store.AnalyticsDB()
	rows, err := db.QueryContext(t.Context(), `SELECT route FROM comfy_analytics_routes`)
	if err != nil {
		t.Fatal(err)
	}
	defer mustClose(t, rows)
	for rows.Next() {
		var route string
		if err := rows.Scan(&route); err != nil {
			t.Fatal(err)
		}
		routes = append(routes, route)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COALESCE(SUM(failed_sign_ins),0), COALESCE(SUM(rate_limited),0), COALESCE(SUM(rejected_tokens),0) FROM comfy_analytics_security`).Scan(&failed, &limited, &rejected); err != nil {
		t.Fatal(err)
	}
	return routes, failed, limited, rejected
}

func TestSiteHealthCountsRoutesWithoutNamingPeopleOrFiles(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	id := uploadPlain(t, h, map[string]string{"visibility": "public"})
	member := h.seedUser("alice")
	h.signIn(t, member.ID)
	for _, path := range []string{"/f/" + id, "/f/" + id, "/f/" + id + "/raw", "/members/" + strconv.FormatInt(member.ID, 10), "/f/%27%3B%20DROP", "/nowhere/alice"} {
		h.get(path)
	}
	routes, _, _, _ := siteHealthRows(t, h)
	if !slices.Contains(routes, "GET /f/{id}") {
		t.Fatalf("file pages were not counted by route pattern: %v", routes)
	}
	for _, route := range routes {
		for _, secret := range []string{id, "alice", "boss", "DROP", strconv.FormatInt(member.ID, 10)} {
			if strings.Contains(route, secret) {
				t.Errorf("route label %q names %q", route, secret)
			}
		}
	}
	var legacy int
	if err := h.store.AnalyticsDB().QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE name = 'comfy_analytics_activity'`).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatal("the per-account analytics table still exists")
	}
}

func TestSecurityCountersRecordRejectedAttempts(t *testing.T) {
	h := newHarness(t)
	h.seedUser("alice")
	h.get("/login")
	for _, password := range []string{"wrong", "<script>", strings.Repeat("x", 4096)} {
		h.postForm("/login", url.Values{"csrf_token": {h.csrf()}, "username": {"alice"}, "password": {password}})
	}
	h.postForm("/login", url.Values{"csrf_token": {h.csrf()}, "username": {"nobody"}, "password": {"x"}})
	h.get("/reset/not-a-real-token")
	_, failed, _, rejected := siteHealthRows(t, h)
	if failed != 4 {
		t.Errorf("failed sign-ins = %d, want 4", failed)
	}
	if rejected < 1 {
		t.Errorf("a bad reset link was not counted")
	}

	shared := httptest.NewRequest("GET", "http://example.test/auth/shared/callback", nil)
	member := h.seedUser("bob")
	h.enableTwoFactorFor(t, member.ID)
	destination, err := h.srv.sharedSignIn(httptest.NewRecorder(), shared, member.ID)
	if err != nil || destination != "/login/2fa" {
		t.Fatal("a shared sign-in skipped the second factor", destination, err)
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
	for _, gone := range []string{"Recent sign-ins", "Most viewed", "Popular", "alice"} {
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
