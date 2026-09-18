// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"net/http"

	"imvault/internal/store"
)

// accountView backs the account settings page.
type accountView struct {
	base
	Status    *store.AccountStatus
	FileCount int
	// ExportableBytes is what a download would weigh before compression, which
	// is the same as the account's stored total.
	ExportableBytes int64
	Error           string
	Notice          string
}

// handleAccountPage shows the account's own settings, including the way out.
func (s *Server) handleAccountPage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	status, err := s.store.StatusFor(r.Context(), user.ID)
	if err != nil {
		s.log.Error("account: load status", "user", user.ID, "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	count, err := s.store.CountFiles(r.Context(), store.FileQuery{OwnerID: &user.ID})
	if err != nil {
		s.log.Error("account: count files", "user", user.ID, "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	view := accountView{
		Status:          status,
		FileCount:       count,
		ExportableBytes: status.User.StorageUsed,
		Error:           r.URL.Query().Get("error"),
		Notice:          r.URL.Query().Get("notice"),
	}
	view.base = s.base(r, "Account")
	// The confirmation field uses Alpine, so the page has to load it.
	view.UseAlpine = true

	s.renderPage(w, http.StatusOK, "account", view)
}

// handleAccountDelete removes the signed-in account and everything it holds.
//
// The confirmation asks for the password, a second factor when one is set, and
// the username typed out. Deleting an account is unrecoverable and takes every
// upload with it, so it is worth being slightly annoying about.
func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if err := s.verifySensitiveAction(w, r, user); err != nil {
		redirectNotice(w, r, "/settings/account", "error", err.Error())
		return
	}

	if typed := trimSpace(r.FormValue("confirm")); typed != user.Username {
		redirectNotice(w, r, "/settings/account", "error",
			"Type your username exactly to confirm. Nothing has been deleted.")
		return
	}

	removed, err := s.deleteAccount(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			redirectNotice(w, r, "/settings/account", "error",
				"You are the last administrator, so this account cannot be deleted. "+
					"Grant somebody else the role first.")
			return
		}
		s.log.Error("account: delete", "user", user.ID, "error", err)
		redirectNotice(w, r, "/settings/account", "error", "Could not delete the account.")
		return
	}

	s.log.Info("account deleted by its owner", "user", user.ID, "files_removed", removed)

	// The session belongs to an account that no longer exists.
	s.clearSessionCookie(w, r)
	s.clearPendingCookie(w, r)

	redirectNotice(w, r, "/", "notice",
		"Your account has been deleted, along with everything you uploaded.")
}

// deleteAccount removes an account and the bytes it stored.
//
// The file rows go with the account by cascade, and the triggers on those
// deletes bring every blob's reference count down as they go. What is left is
// content nothing refers to any more, which the sweep collects: another account
// may share some of it, and its count says so.
func (s *Server) deleteAccount(ctx context.Context, userID int64) (int, error) {
	count, err := s.store.CountFiles(ctx, store.FileQuery{OwnerID: &userID})
	if err != nil {
		return 0, err
	}

	if err := s.store.DeleteUser(ctx, userID); err != nil {
		return 0, err
	}

	s.sweepOrphanedBlobs(ctx)
	return count, nil
}
