// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

// A report is about one album. Deleting that album and giving another the same
// title must not turn the report into a complaint about the newcomer.
func TestAReportDoesNotFollowAReusedSlug(t *testing.T) {
	h := newHarness(t)
	admin := h.provisionAdmin("boss")
	alice, bob, carol := h.seedUser("alice"), h.seedUser("bob"), h.seedUser("carol")

	aliceS := h.sessionFor(t, alice.ID)
	if resp, _ := aliceS.post("/albums", url.Values{"title": {"Holiday"}, "visibility": {"members"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	if resp, _ := h.sessionFor(t, bob.ID).post("/reports", url.Values{
		"target_kind": {"album"}, "target_id": {"holiday"}, "reason": {"spam"},
	}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("report = %d", resp.StatusCode)
	}
	if resp, _ := aliceS.post("/a/holiday/delete", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if resp, _ := h.sessionFor(t, carol.ID).post("/albums", url.Values{"title": {"Holiday"}, "visibility": {"members"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("second album = %d", resp.StatusCode)
	}

	reports, _, err := h.store.ListReports(t.Context(), models.ReportOpen, 10, 0)
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports = %d (%v)", len(reports), err)
	}

	moderator := h.sessionFor(t, admin.ID)
	_, queue := moderator.get("/moderation")
	if strings.Contains(queue, `href="/a/holiday"`) {
		t.Error("the queue points the old report at the new album")
	}
	moderator.post("/moderation/reports/"+itoa64(reports[0].ID)+"/resolve", url.Values{"action": {"remove"}})
	if _, err := h.store.AlbumBySlug(t.Context(), "holiday"); err != nil {
		t.Fatal("resolving a report about a deleted album removed a different album")
	}
}

// Reporting a live album still reaches it, and a moderator can still act on it.
func TestAnAlbumReportStillReachesItsAlbum(t *testing.T) {
	h := newHarness(t)
	admin := h.provisionAdmin("boss")
	alice, bob := h.seedUser("alice"), h.seedUser("bob")
	if resp, _ := h.sessionFor(t, alice.ID).post("/albums", url.Values{"title": {"Holiday"}, "visibility": {"members"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	bobS := h.sessionFor(t, bob.ID)
	bobS.post("/reports", url.Values{"target_kind": {"album"}, "target_id": {"holiday"}, "reason": {"spam"}})
	if resp, _ := bobS.post("/reports", url.Values{"target_kind": {"album"}, "target_id": {"holiday"}, "reason": {"spam"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("second report = %d", resp.StatusCode)
	}
	reports, _, err := h.store.ListReports(t.Context(), models.ReportOpen, 10, 0)
	if err != nil || len(reports) != 1 {
		t.Fatalf("a repeated report was not recognised as a duplicate: %d (%v)", len(reports), err)
	}

	moderator := h.sessionFor(t, admin.ID)
	if _, queue := moderator.get("/moderation"); !strings.Contains(queue, `href="/a/holiday"`) {
		t.Error("the queue does not link the reported album")
	}
	moderator.post("/moderation/reports/"+itoa64(reports[0].ID)+"/resolve", url.Values{"action": {"remove"}})
	if _, err := h.store.AlbumBySlug(t.Context(), "holiday"); err == nil {
		t.Error("removing the reported album left it in place")
	}
}
