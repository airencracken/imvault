// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"imvault/internal/models"
	"imvault/internal/store"
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

	s.renderPage(w, http.StatusOK, "admin", adminDashboardView{
		base:        s.base(r, "Admin"),
		Stats:       stats,
		Users:       users,
		Grid:        s.grid(r, recent, true, false, "/admin/files/%s/delete", "Nothing has been uploaded yet."),
		PendingMail: pendingMail,
		FailedMail:  failedMail,
		MailEnabled: s.mail.Enabled(),
		Notice:      r.URL.Query().Get("notice"),
		Error:       r.URL.Query().Get("error"),
	})
}

// handleAdminUsers lists every account with its usage and controls.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}

	users, total, err := s.store.ListUsers(r.Context(), adminPageSize, (page-1)*adminPageSize)
	if err != nil {
		s.log.Error("admin: list users", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	_, pg := pagination(r, total)
	pg.Page = page

	s.renderPage(w, http.StatusOK, "admin_users", adminUsersView{
		base:       s.base(r, "Users"),
		Users:      users,
		Pagination: pg,
		Notice:     strings.TrimSpace(r.URL.Query().Get("notice")),
		Error:      strings.TrimSpace(r.URL.Query().Get("error")),
	})
}

// handleAdminSetQuota changes an account's storage cap.
func (s *Server) handleAdminSetQuota(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.adminTargetUser(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	raw := strings.TrimSpace(r.FormValue("quota_mb"))
	if raw == "" {
		redirectNotice(w, r, "/admin/users", "error", "Enter a quota in MiB, or 0 for unlimited.")
		return
	}

	megabytes, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || megabytes < 0 {
		redirectNotice(w, r, "/admin/users", "error", "The quota must be a whole number of MiB.")
		return
	}

	quotaBytes := megabytes * 1024 * 1024
	if err := s.store.SetUserQuota(r.Context(), userID, quotaBytes); err != nil {
		s.log.Error("admin: set quota", "user", userID, "error", err)
		redirectNotice(w, r, "/admin/users", "error", "Could not update the quota.")
		return
	}

	message := "Quota cleared; this account is now unlimited."
	if quotaBytes > 0 {
		message = "Quota set to " + models.HumanSize(quotaBytes) + "."
	}
	redirectNotice(w, r, "/admin/users", "notice", message)
}

// handleAdminSetDisabled enables or disables an account.
func (s *Server) handleAdminSetDisabled(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.adminTargetUser(w, r)
	if !ok {
		return
	}
	actor := currentUser(r.Context())

	if userID == actor.ID {
		redirectNotice(w, r, "/admin/users", "error", "You cannot disable your own account.")
		return
	}

	disabled := r.FormValue("disabled") == "1"
	if err := s.store.SetUserDisabled(r.Context(), userID, disabled); err != nil {
		s.log.Error("admin: set disabled", "user", userID, "error", err)
		redirectNotice(w, r, "/admin/users", "error", "Could not update the account.")
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

	message := "Account enabled."
	if disabled {
		message = "Account disabled; its sessions and API keys were revoked."
	}
	redirectNotice(w, r, "/admin/users", "notice", message)
}

// handleAdminSetAdmin grants or revokes administrator rights.
func (s *Server) handleAdminSetAdmin(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.adminTargetUser(w, r)
	if !ok {
		return
	}
	actor := currentUser(r.Context())

	grant := r.FormValue("admin") == "1"
	if userID == actor.ID && !grant {
		redirectNotice(w, r, "/admin/users", "error", "You cannot remove your own administrator rights.")
		return
	}

	if err := s.store.SetUserAdmin(r.Context(), userID, grant); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			redirectNotice(w, r, "/admin/users", "error",
				"The last administrator cannot be demoted; grant somebody else the role first.")
			return
		}
		s.log.Error("admin: set admin", "user", userID, "error", err)
		redirectNotice(w, r, "/admin/users", "error", "Could not update the account.")
		return
	}

	message := "Administrator rights revoked."
	if grant {
		message = "Administrator rights granted."
	}
	redirectNotice(w, r, "/admin/users", "notice", message)
}

// handleAdminDeleteUser removes an account and everything it stored.
func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.adminTargetUser(w, r)
	if !ok {
		return
	}
	actor := currentUser(r.Context())

	if userID == actor.ID {
		redirectNotice(w, r, "/admin/users", "error", "You cannot delete your own account.")
		return
	}

	target, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		redirectNotice(w, r, "/admin/users", "error", "No such account.")
		return
	}

	// Collect the objects before the rows cascade away: the bytes live outside
	// the database and would otherwise be orphaned on disk forever.
	keys, err := s.store.StoredKeysForUser(r.Context(), userID)
	if err != nil {
		s.log.Error("admin: collect file keys", "user", userID, "error", err)
		redirectNotice(w, r, "/admin/users", "error", "Could not delete the account.")
		return
	}

	if err := s.store.DeleteUser(r.Context(), userID); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			redirectNotice(w, r, "/admin/users", "error",
				"The last administrator cannot be deleted.")
			return
		}
		s.log.Error("admin: delete user", "user", userID, "error", err)
		redirectNotice(w, r, "/admin/users", "error", "Could not delete the account.")
		return
	}

	for _, k := range keys {
		s.deleteKeys([]string{k.Object, k.Thumb, k.Preview})
	}

	redirectNotice(w, r, "/admin/users", "notice",
		"Deleted "+target.Username+" and "+strconv.Itoa(len(keys))+" stored file(s).")
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

	s.renderPage(w, http.StatusOK, "admin_files", adminFilesView{
		base:       s.base(r, "All files"),
		Grid:       s.grid(r, files, true, false, "/admin/files/%s/delete", "Nothing has been uploaded yet."),
		Pagination: pg,
	})
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
		// Empty body with an outerHTML swap removes the card.
		w.WriteHeader(http.StatusOK)
		return
	}
	redirectNotice(w, r, "/admin/files", "notice", "Image deleted.")
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
