// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

// moderationHarness builds an instance with an administrator, a moderator, an
// owner of some content, and a bystander who can report it.
func moderationHarness(t *testing.T) (h *harness, owner, reporter, mod *models.User) {
	t.Helper()

	h = newHarness(t)
	h.provisionAdmin("boss")
	owner = h.seedUser("owner")
	reporter = h.seedUser("reporter")
	mod = h.seedUser("mod")

	if err := h.store.SetUserRole(t.Context(), mod.ID, models.RoleModerator); err != nil {
		t.Fatal(err)
	}
	return h, owner, reporter, mod
}

func TestAMemberReportsAndAModeratorRemovesIt(t *testing.T) {
	h, owner, reporter, mod := moderationHarness(t)

	// The owner uploads something public, so the reporter can see it.
	ownerSession := h.sessionFor(t, owner.ID)
	fileID := uploadAs(t, ownerSession, "bad.png", "public")

	reporterSession := h.sessionFor(t, reporter.ID)

	// The report control is offered on somebody else's upload.
	_, page := reporterSession.get("/f/" + fileID)
	if !strings.Contains(page, `action="/reports"`) {
		t.Error("the report form is not offered on another member's upload")
	}

	resp, _ := reporterSession.post("/reports", url.Values{
		"target_kind": {"file"},
		"target_id":   {fileID},
		"reason":      {"abuse"},
		"note":        {"this is not allowed here"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("report = %d, want 303", resp.StatusCode)
	}

	// The moderator sees it waiting, with the reason and the note.
	modSession := h.sessionFor(t, mod.ID)
	resp, queue := modSession.get("/moderation")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the queue = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(queue, "Harassment or abuse") {
		t.Error("the queue does not show the reason")
	}
	if !strings.Contains(queue, "this is not allowed here") {
		t.Error("the queue does not show the reporter's note")
	}
	if !strings.Contains(queue, "reporter") {
		t.Error("the queue does not attribute the report")
	}

	// Removing the content closes the report.
	reportID := lastReportID(t, h)
	resp, _ = modSession.post("/moderation/reports/"+itoa64(reportID)+"/resolve", url.Values{
		"action":     {"remove"},
		"resolution": {"removed for abuse"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("resolve = %d, want 303", resp.StatusCode)
	}

	if _, err := h.store.FileByID(t.Context(), fileID); err == nil {
		t.Error("the reported file is still there")
	}
	report, err := h.store.ReportByID(t.Context(), reportID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != models.ReportActioned {
		t.Errorf("the report is %q, want actioned", report.Status)
	}
	if report.Resolution != "removed for abuse" {
		t.Errorf("resolution = %q", report.Resolution)
	}

	// The queue is empty, and the log explains what happened.
	_, queue = modSession.get("/moderation")
	if strings.Contains(queue, "Harassment or abuse") {
		t.Error("the resolved report is still in the queue")
	}

	_, logPage := modSession.get("/moderation/log")
	if !strings.Contains(logPage, "bad.png") {
		t.Error("the log does not name the removed file")
	}
	if !strings.Contains(logPage, "mod") {
		t.Error("the log does not attribute the removal")
	}
	if !strings.Contains(logPage, "removed for abuse") {
		t.Error("the log does not record the reason")
	}
}

func TestDismissingAReportKeepsTheContent(t *testing.T) {
	h, owner, reporter, mod := moderationHarness(t)

	ownerSession := h.sessionFor(t, owner.ID)
	fileID := uploadAs(t, ownerSession, "fine.png", "public")

	reporterSession := h.sessionFor(t, reporter.ID)
	reporterSession.post("/reports", url.Values{
		"target_kind": {"file"},
		"target_id":   {fileID},
		"reason":      {"spam"},
	})

	modSession := h.sessionFor(t, mod.ID)
	reportID := lastReportID(t, h)
	resp, _ := modSession.post("/moderation/reports/"+itoa64(reportID)+"/resolve", url.Values{
		"action":     {"dismiss"},
		"resolution": {"not spam"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("dismiss = %d, want 303", resp.StatusCode)
	}

	// The content survives, which is the difference between the two actions.
	if _, err := h.store.FileByID(t.Context(), fileID); err != nil {
		t.Errorf("dismissing removed the file: %v", err)
	}
	report, err := h.store.ReportByID(t.Context(), reportID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != models.ReportDismissed {
		t.Errorf("the report is %q, want dismissed", report.Status)
	}
}

func TestReportingIsRefusedWhereItMakesNoSense(t *testing.T) {
	h, owner, reporter, mod := moderationHarness(t)

	ownerSession := h.sessionFor(t, owner.ID)
	fileID := uploadAs(t, ownerSession, "mine.png", "public")

	// Your own upload: you have Delete.
	resp, _ := ownerSession.post("/reports", url.Values{
		"target_kind": {"file"},
		"target_id":   {fileID},
		"reason":      {"spam"},
	})
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "use+Delete") &&
		!strings.Contains(loc, "use%20Delete") {
		t.Errorf("reporting your own upload was not refused: %q", loc)
	}
	if count := countOpenReports(t, h); count != 0 {
		t.Errorf("%d reports were recorded", count)
	}

	// A moderator can remove it directly, so reporting it is busywork.
	modSession := h.sessionFor(t, mod.ID)
	resp, _ = modSession.post("/reports", url.Values{
		"target_kind": {"file"},
		"target_id":   {fileID},
		"reason":      {"spam"},
	})
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "directly") {
		t.Errorf("a moderator's report was not refused: %q", loc)
	}

	// The same report twice fills the queue with duplicates.
	reporterSession := h.sessionFor(t, reporter.ID)
	form := url.Values{"target_kind": {"file"}, "target_id": {fileID}, "reason": {"spam"}}
	reporterSession.post("/reports", form)
	reporterSession.post("/reports", form)
	if count := countOpenReports(t, h); count != 1 {
		t.Errorf("%d open reports after reporting twice, want 1", count)
	}

	// A second member may still report the same thing: it is not more
	// persuasive, but it is a different person's complaint.
	other := h.seedUser("other")
	otherSession := h.sessionFor(t, other.ID)
	otherSession.post("/reports", form)
	if count := countOpenReports(t, h); count != 2 {
		t.Errorf("%d open reports after a second member reports, want 2", count)
	}
}

func TestTheAuditTrailRecordsOtherPeoplesRemovals(t *testing.T) {
	h, owner, _, mod := moderationHarness(t)

	ownerSession := h.sessionFor(t, owner.ID)
	moderatorFile := uploadAs(t, ownerSession, "moderated.png", "public")
	ownerFile := uploadAs(t, ownerSession, "tidy.png", "public")

	// An owner clearing out their own gallery is housekeeping, not moderation,
	// and the log should not fill up with it.
	ownerSession.post("/f/"+ownerFile+"/delete", url.Values{})
	entries, _, err := h.store.ListModerationLog(t.Context(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("an owner's own deletion was logged: %d entries", len(entries))
	}

	// A moderator removing somebody else's content is exactly what the log is
	// for.
	modSession := h.sessionFor(t, mod.ID)
	if resp, _ := modSession.post("/admin/files/"+moderatorFile+"/delete", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("moderator delete = %d, want 303", resp.StatusCode)
	}

	entries, _, err = h.store.ListModerationLog(t.Context(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d log entries, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Action != models.ActionRemoveFile {
		t.Errorf("action = %q", entry.Action)
	}
	if entry.TargetLabel != "moderated.png" {
		t.Errorf("target label = %q, want the filename", entry.TargetLabel)
	}
	if entry.ActorName != "mod" {
		t.Errorf("actor = %q, want mod", entry.ActorName)
	}
}

func TestLogEntriesSurviveTheirAccountsAndTargets(t *testing.T) {
	h, owner, _, mod := moderationHarness(t)

	ownerSession := h.sessionFor(t, owner.ID)
	fileID := uploadAs(t, ownerSession, "gone.png", "public")

	modSession := h.sessionFor(t, mod.ID)
	modSession.post("/f/"+fileID+"/delete", url.Values{})

	// Remove the moderator's account. The record has to keep saying who did it,
	// or it is not an audit trail.
	if _, err := h.store.DB().ExecContext(t.Context(), `DELETE FROM users WHERE id = ?`, mod.ID); err != nil {
		t.Fatal(err)
	}

	entries, _, err := h.store.ListModerationLog(t.Context(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d entries, want 1", len(entries))
	}
	if entries[0].ActorName != "mod" {
		t.Errorf("actor name = %q after the account was deleted", entries[0].ActorName)
	}
	if entries[0].ActorID != nil {
		t.Error("the actor id survived the account")
	}
	if entries[0].TargetLabel != "gone.png" {
		t.Errorf("target label = %q after the file was deleted", entries[0].TargetLabel)
	}
}

func TestOnlyModeratorsReachTheQueue(t *testing.T) {
	h, _, reporter, _ := moderationHarness(t)

	memberSession := h.sessionFor(t, reporter.ID)
	for _, path := range []string{"/moderation", "/moderation/log"} {
		if resp, _ := memberSession.get(path); resp.StatusCode != http.StatusForbidden {
			t.Errorf("a member reached %s: %d", path, resp.StatusCode)
		}
	}

	// And an anonymous visitor is sent to sign in, not shown a 403.
	if resp, _ := h.newSession(t).get("/moderation"); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("an anonymous visitor got %d for the queue", resp.StatusCode)
	}
}

// lastReportID is the most recent report's id, which is the one the queue puts
// first.
func lastReportID(t *testing.T, h *harness) int64 {
	t.Helper()

	reports, _, err := h.store.ListReports(t.Context(), models.ReportOpen, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) == 0 {
		t.Fatal("no open reports")
	}
	return reports[0].ID
}

func countOpenReports(t *testing.T, h *harness) int {
	t.Helper()

	n, err := h.store.CountOpenReports(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return n
}
