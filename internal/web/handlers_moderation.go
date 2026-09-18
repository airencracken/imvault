// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"imvault/internal/models"
	"imvault/internal/store"
)

// moderationPageSize is how many reports or log entries a page shows.
const moderationPageSize = 50

// maxReportNote bounds what a reporter and a moderator may write.
const maxReportNote = 500

// reportRow is one report with its target resolved for display.
type reportRow struct {
	Report      *models.Report
	TargetLabel string
	TargetURL   string
	TargetThumb string
	// TargetGone means the content was removed before the queue reached it,
	// which is the usual outcome for an obvious report and worth saying.
	TargetGone bool
	Owner      string
	Age        string
}

type moderationView struct {
	base
	Rows       []reportRow
	Closed     int
	Pagination paginationView
}

type moderationLogView struct {
	base
	Entries    []*models.ModerationEntry
	Pagination paginationView
}

// recordModeration appends to the audit trail, reporting a failure without
// failing the action it describes.
func (s *Server) recordModeration(ctx context.Context, actor *models.User,
	action models.ModerationAction, kind models.TargetKind, targetID, targetLabel, reason string) {

	err := s.store.RecordModeration(ctx, store.ModerationEntry{
		Actor:       actor,
		Action:      action,
		TargetKind:  kind,
		TargetID:    targetID,
		TargetLabel: targetLabel,
		Reason:      reason,
	})
	if err != nil {
		// The removal already happened. Losing the log line leaves a gap in the
		// record; refusing the removal would leave the content up, which is the
		// worse of the two.
		s.log.Error("moderation log write failed",
			"action", string(action), "target", targetID, "error", err)
	}
}

// recordFileRemoval logs a file removal when the actor is not its owner.
//
// Somebody deleting their own upload is housekeeping, not moderation, and
// filling the log with it would bury the entries that matter.
func (s *Server) recordFileRemoval(ctx context.Context, actor *models.User, file *models.File, reason string) {
	if ownsFile(actor, file) {
		return
	}
	s.recordModeration(ctx, actor, models.ActionRemoveFile,
		models.TargetFile, file.ID, file.OriginalName, reason)
}

// recordAlbumRemoval logs an album removal when the actor is not its owner.
func (s *Server) recordAlbumRemoval(ctx context.Context, actor *models.User, album *models.Album, reason string) {
	if ownsAlbum(actor, album) {
		return
	}
	s.recordModeration(ctx, actor, models.ActionRemoveAlbum,
		models.TargetAlbum, album.Slug, album.Title, reason)
}

// handleReport accepts a member's report about a file or an album.
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	kind := models.TargetKind(strings.TrimSpace(r.FormValue("target_kind")))
	targetID := strings.TrimSpace(r.FormValue("target_id"))
	reason := models.ParseReportReason(r.FormValue("reason"))
	note := models.Truncate(strings.TrimSpace(r.FormValue("note")), maxReportNote)

	// Where to send them back to, resolved from the target rather than trusting
	// a redirect carried in the form, and whether it is theirs.
	back := "/"
	isOwn := false
	switch kind {
	case models.TargetFile:
		file, err := s.store.FileByID(r.Context(), targetID)
		if err != nil || !canViewFile(user, file) {
			s.notFound(w, r, "That image does not exist.")
			return
		}
		back = "/f/" + file.ID
		isOwn = ownsFile(user, file)
	case models.TargetAlbum:
		album, err := s.store.AlbumBySlug(r.Context(), targetID)
		if err != nil || !canViewAlbum(user, album) {
			s.notFound(w, r, "No album by that name.")
			return
		}
		back = "/a/" + album.Slug
		isOwn = ownsAlbum(user, album)
	default:
		http.Error(w, "invalid report", http.StatusBadRequest)
		return
	}

	// Reporting your own content is not a thing, and neither is reporting
	// something as a moderator, who can simply remove it.
	if isOwn {
		redirectNotice(w, r, back, "error", "That is yours; use Delete instead.")
		return
	}
	if canModerateContent(user) {
		redirectNotice(w, r, back, "error",
			"You can remove it directly rather than reporting it.")
		return
	}

	created, err := s.store.CreateReport(r.Context(), kind, targetID, user.ID, user.Username, reason, note)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			redirectNotice(w, r, back, "notice",
				"You have already reported this. A moderator will look at it.")
			return
		}
		s.log.Error("create report", "kind", string(kind), "target", targetID, "error", err)
		redirectNotice(w, r, back, "error", "Could not record the report.")
		return
	}

	s.log.Info("content reported",
		"reporter", user.ID,
		"kind", string(kind),
		"target", targetID,
		"reason", string(reason),
		"report", created.ID)

	redirectNotice(w, r, back, "notice",
		"Thank you. A moderator will look at it.")
}

// reportRowFor resolves a report's target for display.
func (s *Server) reportRowFor(r *http.Request, report *models.Report) reportRow {
	row := reportRow{Report: report, Age: models.HumanTime(report.CreatedAt)}

	switch report.TargetKind {
	case models.TargetAlbum:
		album, err := s.store.AlbumBySlug(r.Context(), report.TargetID)
		if err != nil {
			row.TargetGone = true
			row.TargetLabel = report.TargetID
			return row
		}
		row.TargetLabel = album.Title
		row.TargetURL = "/a/" + album.Slug
		row.Owner = album.Username
	case models.TargetFile:
		file, err := s.store.FileByID(r.Context(), report.TargetID)
		if err != nil {
			row.TargetGone = true
			row.TargetLabel = report.TargetID
			return row
		}
		row.TargetLabel = file.OriginalName
		row.TargetURL = "/f/" + file.ID
		row.TargetThumb = "/f/" + file.ID + "/thumb"
		row.Owner = file.Username
	}
	return row
}

// handleModeration shows the open reports, oldest first.
func (s *Server) handleModeration(w http.ResponseWriter, r *http.Request) {
	open := s.countReports(r, models.ReportOpen)
	all := s.countReports(r, "")

	page, pg := pagination(r, open)

	reports, _, err := s.store.ListReports(r.Context(), models.ReportOpen,
		moderationPageSize, (page-1)*moderationPageSize)
	if err != nil {
		s.log.Error("moderation: list reports", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	rows := make([]reportRow, 0, len(reports))
	for _, report := range reports {
		rows = append(rows, s.reportRowFor(r, report))
	}

	view := moderationView{
		base:       s.base(r, "Reports"),
		Rows:       rows,
		Closed:     all - open,
		Pagination: pg,
	}
	s.renderPage(w, http.StatusOK, "moderation", view)
}

// countReports counts reports with a status, or all of them when status is
// empty. A failure is logged rather than fatal: a wrong number is worth less
// than the page.
func (s *Server) countReports(r *http.Request, status models.ReportStatus) int {
	_, total, err := s.store.ListReports(r.Context(), status, 1, 0)
	if err != nil {
		s.log.Error("moderation: count reports", "status", string(status), "error", err)
	}
	return total
}

// handleModerationLog shows the audit trail.
func (s *Server) handleModerationLog(w http.ResponseWriter, r *http.Request) {
	_, total, err := s.store.ListModerationLog(r.Context(), 1, 0)
	if err != nil {
		s.log.Error("moderation: count log", "error", err)
	}
	page, pg := pagination(r, total)

	entries, _, err := s.store.ListModerationLog(r.Context(), moderationPageSize, (page-1)*moderationPageSize)
	if err != nil {
		s.log.Error("moderation: list log", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	view := moderationLogView{
		base:       s.base(r, "Moderation log"),
		Entries:    entries,
		Pagination: pg,
	}
	s.renderPage(w, http.StatusOK, "moderation_log", view)
}

// handleResolveReport works one report: remove the content, or dismiss it.
func (s *Server) handleResolveReport(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r.Context())

	reportID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid report", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	report, err := s.store.ReportByID(r.Context(), reportID)
	if err != nil {
		s.notFound(w, r, "That report does not exist.")
		return
	}
	if !report.Open() {
		redirectNotice(w, r, "/moderation", "notice", "That report has already been dealt with.")
		return
	}

	note := models.Truncate(strings.TrimSpace(r.FormValue("resolution")), maxReportNote)
	action := strings.TrimSpace(r.FormValue("action"))

	// Name the target before acting, so the decision's log entry can still say
	// what it was about once the content is gone.
	targetLabel := s.reportLabel(r, report)

	var (
		status   models.ReportStatus
		recorded models.ModerationAction
		message  string
	)

	switch action {
	case "remove":
		label, err := s.removeReportedTarget(r.Context(), actor, report, note)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// Already gone, which is the outcome the moderator wanted.
				status = models.ReportActioned
				recorded = models.ActionResolveReport
				message = "The content was already gone. The report is closed."
				break
			}
			s.log.Error("moderation: remove reported content",
				"report", report.ID, "target", report.TargetID, "error", err)
			redirectNotice(w, r, "/moderation", "error", "Could not remove the content.")
			return
		}
		status = models.ReportActioned
		recorded = models.ActionResolveReport
		targetLabel = label
		message = fmt.Sprintf("Removed %s and closed the report.", label)
	case "dismiss":
		status = models.ReportDismissed
		recorded = models.ActionDismissReport
		message = "Report dismissed."
	default:
		redirectNotice(w, r, "/moderation", "error", "Choose whether to remove the content or dismiss the report.")
		return
	}

	if err := s.store.ResolveReport(r.Context(), report.ID, actor.ID, status, note); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			redirectNotice(w, r, "/moderation", "notice", "Another moderator got there first.")
			return
		}
		s.log.Error("moderation: resolve report", "report", report.ID, "error", err)
		redirectNotice(w, r, "/moderation", "error", "The report could not be closed.")
		return
	}

	// The removal itself was logged as it happened; this records the decision,
	// so the trail reads as a sequence rather than a single unexplained removal.
	s.recordModeration(r.Context(), actor, recorded,
		report.TargetKind, report.TargetID, targetLabel, note)

	s.log.Info("report resolved",
		"actor", actor.ID,
		"report", report.ID,
		"status", string(status),
		"target", report.TargetID)

	redirectNotice(w, r, "/moderation", "notice", message)
}

// reportLabel names the thing a report is about, for the log entry. It falls
// back to the stored id when the content is already gone.
func (s *Server) reportLabel(r *http.Request, report *models.Report) string {
	switch report.TargetKind {
	case models.TargetAlbum:
		if album, err := s.store.AlbumBySlug(r.Context(), report.TargetID); err == nil {
			return album.Title
		}
	case models.TargetFile:
		if file, err := s.store.FileByID(r.Context(), report.TargetID); err == nil {
			return file.OriginalName
		}
	}
	return report.TargetID
}

// removeReportedTarget deletes whatever a report is about, returning a label for
// the notice.
func (s *Server) removeReportedTarget(ctx context.Context, actor *models.User,
	report *models.Report, note string) (string, error) {

	reason := note
	if reason == "" {
		reason = "reported: " + report.Reason.Label()
	}

	switch report.TargetKind {
	case models.TargetAlbum:
		album, err := s.store.AlbumBySlug(ctx, report.TargetID)
		if err != nil {
			return "", err
		}
		if err := s.store.DeleteAlbum(ctx, album.ID); err != nil {
			return "", err
		}
		s.recordModeration(ctx, actor, models.ActionRemoveAlbum,
			models.TargetAlbum, album.Slug, album.Title, reason)
		return album.Title, nil
	case models.TargetFile:
		file, err := s.store.FileByID(ctx, report.TargetID)
		if err != nil {
			return "", err
		}
		if err := s.deleteFileAndRelease(ctx, file); err != nil {
			return "", err
		}
		s.recordModeration(ctx, actor, models.ActionRemoveFile,
			models.TargetFile, file.ID, file.OriginalName, reason)
		return file.OriginalName, nil
	}

	return "", fmt.Errorf("moderation: %q is not something to remove", report.TargetKind)
}
