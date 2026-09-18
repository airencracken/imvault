// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"imvault/internal/models"
	"imvault/internal/store"
	"imvault/internal/tokens"
)

// adminPageSize is how many rows the admin listings show.
const adminPageSize = 50

// requireAdmin guards the admin UI.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if !currentUser(r.Context()).IsAdmin {
			s.renderPage(w, http.StatusForbidden, "notfound", errorView{
				base:    s.base(r, "Not permitted"),
				Code:    "403",
				Message: "That area is for administrators.",
			})
			return
		}
		next(w, r)
	})
}

// adminTargetUser resolves the {id} path value, writing the error response
// itself.
func (s *Server) adminTargetUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid account", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// handleAdminDashboard shows instance-wide totals and the heaviest accounts.
func (s *Server) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context())
	if err != nil {
		s.log.Error("admin: stats", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	users, _, err := s.store.ListUsers(r.Context(), 8, 0)
	if err != nil {
		s.log.Error("admin: top users", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	recent, err := s.store.ListFiles(r.Context(), store.FileQuery{Limit: 12})
	if err != nil {
		s.log.Error("admin: recent files", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	pendingMail, failedMail, err := s.store.MailBacklog(r.Context())
	if err != nil {
		s.log.Error("admin: mail backlog", "error", err)
	}

	blobs, err := s.store.BlobSummary(r.Context())
	if err != nil {
		s.log.Error("admin: blob summary", "error", err)
	}

	view := adminDashboardView{
		Stats:       stats,
		Users:       users,
		Grid:        s.grid(r, recent, true, false, "/admin/files/%s/delete", "Nothing has been uploaded yet."),
		PendingMail: pendingMail,
		FailedMail:  failedMail,
		MailEnabled: s.mail.Enabled(),
		Blobs:       blobs,
		Notice:      noticeFromQuery(r),
	}
	view.base = s.base(r, "Admin")
	view.UseAlpine = true

	s.renderPage(w, http.StatusOK, "admin", view)
}

// handleAdminUsers lists every account with its usage and controls.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}

	if page == 1 {
		s.renderAdminUsersPage(w, r, http.StatusOK, noticeFromQuery(r))
		return
	}

	users, total, err := s.store.ListUsers(r.Context(), adminPageSize, (page-1)*adminPageSize)
	if err != nil {
		s.log.Error("admin: list users", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	_, pg := pagination(r, total)
	pg.Page = page

	view := adminUsersView{
		Rows:       s.adminUserRows(r, users),
		Pagination: pg,
		Notice:     noticeFromQuery(r),
	}
	view.base = s.base(r, "Users")
	view.UseAlpine = true

	s.renderPage(w, http.StatusOK, "admin_users", view)
}

// handleAdminSetLimits changes an account's storage cap and its single-file
// ceiling together, since they are the same decision seen from two angles.
func (s *Server) handleAdminSetLimits(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.adminTargetUser(w, r)
	if !ok {
		return
	}

	user, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		s.renderAdminRow(w, "admin_user_row", nil, adminNotice{Text: "No such account.", Error: true})
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	quotaMB, ok := adminMegabytes(r, "quota_mb")
	if !ok {
		s.adminRespond(w, r, user, adminNotice{},
			"Both limits must be whole numbers of MiB. Nothing was changed.", "/admin/users")
		return
	}

	fileMB, ok := adminMegabytes(r, "max_file_mb")
	if !ok {
		s.adminRespond(w, r, user, adminNotice{},
			"Both limits must be whole numbers of MiB. Nothing was changed.", "/admin/users")
		return
	}

	quotaBytes := quotaMB * 1024 * 1024
	fileBytes := fileMB * 1024 * 1024

	if err := s.store.SetUserQuota(r.Context(), userID, quotaBytes); err != nil {
		s.log.Error("admin: set quota", "user", userID, "error", err)
		s.adminRespond(w, r, user, adminNotice{},
			"Could not update the limits.", "/admin/users")
		return
	}
	if err := s.store.SetUserFileLimit(r.Context(), userID, fileBytes); err != nil {
		s.log.Error("admin: set file limit", "user", userID, "error", err)
		s.adminRespond(w, r, user, adminNotice{},
			"Could not update the limits.", "/admin/users")
		return
	}

	updated := s.reloadAdminUser(r, userID)
	s.adminRespond(w, r, updated, adminNotice{Text: limitsMessage(user.Username, quotaBytes, fileBytes)}, "", "/admin/users")
}

// adminMegabytes reads a MiB field, treating a blank or negative value as zero
// rather than as an error: blank means "no override" and zero means "no limit".
func adminMegabytes(r *http.Request, field string) (int64, bool) {
	raw := strings.TrimSpace(r.FormValue(field))
	if raw == "" {
		return 0, true
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

// limitsMessage describes what was just set, in the terms each limit is thought
// about: unlimited, or no override.
func limitsMessage(username string, quotaBytes, fileBytes int64) string {
	quota := "unlimited"
	if quotaBytes > 0 {
		quota = models.HumanSize(quotaBytes)
	}

	file := "the instance default"
	if fileBytes > 0 {
		file = models.HumanSize(fileBytes)
	}

	return fmt.Sprintf("%s: total storage %s, largest single file %s.", username, quota, file)
}

// handleAdminSetDisabled enables or disables an account.
func (s *Server) handleAdminSetDisabled(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.adminTargetUser(w, r)
	if !ok {
		return
	}

	user, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		s.renderAdminRow(w, "admin_user_row", nil, adminNotice{Text: "No such account.", Error: true})
		return
	}

	if userID == currentUser(r.Context()).ID {
		s.adminRespond(w, r, user, adminNotice{},
			"You cannot disable your own account.", "/admin/users")
		return
	}

	disabled := r.FormValue("disabled") == "1"
	if err := s.store.SetUserDisabled(r.Context(), userID, disabled); err != nil {
		s.log.Error("admin: set disabled", "user", userID, "error", err)
		s.adminRespond(w, r, user, adminNotice{},
			"Could not update the account.", "/admin/users")
		return
	}

	// A disabled account must not keep a live session or working keys.
	if disabled {
		if err := s.store.DeleteSessionsForUser(r.Context(), userID); err != nil {
			s.log.Error("admin: revoke sessions", "user", userID, "error", err)
		}
		if err := s.store.DeleteAPIKeysForUser(r.Context(), userID); err != nil {
			s.log.Error("admin: revoke keys", "user", userID, "error", err)
		}
	}

	message := user.Username + " can sign in again."
	if disabled {
		message = user.Username + " is disabled; their sessions and API keys were revoked."
	}

	updated := s.reloadAdminUser(r, userID)
	s.adminRespond(w, r, updated, adminNotice{Text: message}, "", "/admin/users")
}

// handleAdminSetAdmin grants or revokes administrator rights.
func (s *Server) handleAdminSetAdmin(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.adminTargetUser(w, r)
	if !ok {
		return
	}

	user, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		s.renderAdminRow(w, "admin_user_row", nil, adminNotice{Text: "No such account.", Error: true})
		return
	}

	grant := r.FormValue("admin") == "1"
	if userID == currentUser(r.Context()).ID && !grant {
		s.adminRespond(w, r, user, adminNotice{},
			"You cannot remove your own administrator rights.", "/admin/users")
		return
	}

	if err := s.store.SetUserAdmin(r.Context(), userID, grant); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			s.adminRespond(w, r, user, adminNotice{},
				"The last administrator cannot be demoted; grant somebody else the role first.",
				"/admin/users")
			return
		}
		s.log.Error("admin: set admin", "user", userID, "error", err)
		s.adminRespond(w, r, user, adminNotice{},
			"Could not update the account.", "/admin/users")
		return
	}

	message := "Administrator rights revoked from " + user.Username + "."
	if grant {
		message = user.Username + " is now an administrator."
	}

	updated := s.reloadAdminUser(r, userID)
	s.adminRespond(w, r, updated, adminNotice{Text: message}, "", "/admin/users")
}

// handleAdminDeleteUser removes an account and everything it stored.
func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.adminTargetUser(w, r)
	if !ok {
		return
	}

	if userID == currentUser(r.Context()).ID {
		user, _ := s.store.UserByID(r.Context(), userID)
		s.adminRespond(w, r, user, adminNotice{},
			"You cannot delete your own account.", "/admin/users")
		return
	}

	target, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		s.renderAdminRow(w, "admin_user_row", nil, adminNotice{Text: "No such account.", Error: true})
		return
	}

	removed, err := s.deleteAccount(r.Context(), userID)
	if err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			s.adminRespond(w, r, target, adminNotice{},
				"The last administrator cannot be deleted.", "/admin/users")
			return
		}
		s.log.Error("admin: delete user", "user", userID, "error", err)
		s.adminRespond(w, r, target, adminNotice{},
			"Could not delete the account.", "/admin/users")
		return
	}

	message := fmt.Sprintf("Deleted %s and %d stored file(s).", target.Username, removed)
	s.adminRespond(w, r, nil, adminNotice{Text: message}, "", "/admin/users")
}

// handleAdminIssueReset mints a reset link and shows it to the administrator.
//
// The link is rendered into the response rather than redirected to, so it never
// ends up in a URL, a browser history entry or a server log.
func (s *Server) handleAdminIssueReset(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.adminTargetUser(w, r)
	if !ok {
		return
	}

	target, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		s.renderAdminRow(w, "admin_user_row", nil, adminNotice{Text: "No such account.", Error: true})
		return
	}

	if err := s.store.DeleteAuthTokensForUser(r.Context(), target.ID, store.TokenPasswordReset); err != nil {
		s.log.Error("admin reset: clear tokens", "user", target.ID, "error", err)
	}

	token, hash := tokens.New()
	expires := time.Now().UTC().Add(s.cfg.PasswordResetTTL)
	if err := s.store.CreateAuthToken(r.Context(), target.ID, store.TokenPasswordReset, hash, expires); err != nil {
		s.log.Error("admin reset: create token", "user", target.ID, "error", err)
		s.adminRespond(w, r, target, adminNotice{},
			"Could not issue a reset link.", "/admin/users")
		return
	}

	s.log.Info("admin issued a password reset link",
		"actor", currentUser(r.Context()).ID, "user", target.ID)

	notice := adminNotice{
		Text: fmt.Sprintf("Reset link for %s. Hand it over out of band: it works once and expires in %s.",
			target.Username, humanDuration(s.cfg.PasswordResetTTL)),
		Link:      s.absoluteURL(r, resetPath+token),
		LinkLabel: "Reset link for " + target.Username,
	}

	// The link exists only in this response, so the non-JavaScript path has to
	// render a page rather than redirect: a redirect would lose it.
	if !isHTMX(r) {
		s.renderAdminUsersPage(w, r, http.StatusOK, notice)
		return
	}
	s.adminRespond(w, r, target, notice, "", "/admin/users")
}

// handleAdminFiles lists every upload for moderation.
func (s *Server) handleAdminFiles(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}

	filter := store.FileQuery{Limit: adminPageSize, Offset: (page - 1) * adminPageSize}

	total, err := s.store.CountFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("admin: count files", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	files, err := s.store.ListFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("admin: list files", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	_, pg := pagination(r, total)
	pg.Page = page

	view := adminFilesView{
		Grid:       s.grid(r, files, true, false, "/admin/files/%s/delete", "Nothing has been uploaded yet."),
		Pagination: pg,
	}
	view.base = s.base(r, "All files")
	view.UseAlpine = true

	s.renderPage(w, http.StatusOK, "admin_files", view)
}

// handleAdminDeleteFile removes any account's upload, adjusting its owner's
// usage.
func (s *Server) handleAdminDeleteFile(w http.ResponseWriter, r *http.Request) {
	file, err := s.store.FileByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, "That image does not exist.")
		return
	}

	if err := s.deleteFileAndRelease(r.Context(), file); err != nil {
		s.log.Error("admin: delete file", "id", file.ID, "error", err)
		http.Error(w, "could not delete the image", http.StatusInternalServerError)
		return
	}

	if isHTMX(r) {
		// An empty body with an outerHTML swap removes the card.
		w.WriteHeader(http.StatusOK)
		return
	}
	redirectNotice(w, r, "/admin/files", "notice", "Image deleted.")
}

// handleAdminRecomputeStorage recalculates every account's usage from the files
// table, repairing any drift left by a crash mid-upload.
func (s *Server) handleAdminRecomputeStorage(w http.ResponseWriter, r *http.Request) {
	corrected, err := s.store.RecomputeStorageUsage(r.Context())
	if err != nil {
		s.log.Error("admin: recompute storage", "error", err)
		s.adminMaintenance(w, r, adminNotice{Text: "Could not recalculate storage usage.", Error: true})
		return
	}

	s.log.Info("admin recalculated storage usage",
		"actor", currentUser(r.Context()).ID, "accounts_corrected", corrected)

	var message string
	switch corrected {
	case 0:
		message = "Storage usage was already accurate."
	case 1:
		message = "Corrected the usage recorded for 1 account."
	default:
		message = fmt.Sprintf("Corrected the usage recorded for %d accounts.", corrected)
	}

	s.adminMaintenance(w, r, adminNotice{Text: message})
}

// handleAdminRecomputeBlobs recalculates the de-duplication reference counts.
func (s *Server) handleAdminRecomputeBlobs(w http.ResponseWriter, r *http.Request) {
	corrected, err := s.store.RecomputeBlobRefcounts(r.Context())
	if err != nil {
		s.log.Error("admin: recompute blob refcounts", "error", err)
		s.adminMaintenance(w, r, adminNotice{Text: "Could not recalculate the reference counts.", Error: true})
		return
	}

	// Anything the repair revealed as unreferenced is now collectable.
	s.sweepOrphanedBlobs(r.Context())

	s.log.Info("admin recalculated de-duplication counts",
		"actor", currentUser(r.Context()).ID, "content_corrected", corrected)

	message := "The reference counts were already accurate."
	if corrected == 1 {
		message = "Corrected the reference count for 1 piece of content."
	} else if corrected > 1 {
		message = fmt.Sprintf("Corrected the reference count for %d pieces of content.", corrected)
	}
	s.adminMaintenance(w, r, adminNotice{Text: message})
}

// adminMaintenance reports a maintenance action, updating only the notice.
func (s *Server) adminMaintenance(w http.ResponseWriter, r *http.Request, notice adminNotice) {
	if !isHTMX(r) {
		key := "notice"
		if notice.Error {
			key = "error"
		}
		redirectNotice(w, r, "/admin", key, notice.Text)
		return
	}
	s.renderAdminRow(w, "", nil, notice)
}
