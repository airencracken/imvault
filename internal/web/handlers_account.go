// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"github.com/airencracken/comfylib/displaytime"
	"net/http"
	"strconv"

	"imvault/internal/models"
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
	// Identities are the provider accounts connected to this one.
	Identities []*models.Identity
	Error      string
	Notice     string
	// Credentials says how the account proves itself, which decides whether
	// deletion asks for a password and whether a connection may be removed.
	Credentials credentialState
}

// handleAccountPage shows the account's own settings, including the way out.
func (s *Server) handleAccountPage(w http.ResponseWriter, r *http.Request) {
	s.accountPage(w, r, 200, nil, "")
}
func (s *Server) accountPage(w http.ResponseWriter, r *http.Request, statusCode int, draft *string, problem string) {
	user := currentUser(r.Context())

	status, err := s.store.StatusFor(r.Context(), user.ID)
	if err != nil {
		s.log.Error("account: load status", "user", user.ID, "error", err)
		s.fail(w, r, http.StatusInternalServerError, "Something went wrong on our side. Please try again.")
		return
	}

	count, err := s.store.CountFiles(r.Context(), store.FileQuery{OwnerID: &user.ID})
	if err != nil {
		s.log.Error("account: count files", "user", user.ID, "error", err)
		s.fail(w, r, http.StatusInternalServerError, "Something went wrong on our side. Please try again.")
		return
	}

	identities, err := s.store.IdentitiesByUser(r.Context(), user.ID)
	if err != nil {
		s.log.Error("account: identities", "user", user.ID, "error", err)
	}

	view := accountView{
		Status:          status,
		FileCount:       count,
		ExportableBytes: status.User.StorageUsed,
		Identities:      identities,
	}
	view.Notice, view.Error = s.flash(r)
	view.Credentials = s.credentialsFor(r, user, "/settings/account")
	view.base = s.base(r, "Account")
	// The confirmation field uses Alpine, so the page has to load it.
	view.UseAlpine = true
	if draft != nil {
		view.TimezoneControl.Name = *draft
	}
	view.TimezoneControl.Error = problem
	if r.URL.Query().Get("saved") == "timezone" {
		view.TimezoneControl.Notice = "Your display timezone has been saved."
	}

	s.renderPage(w, r, statusCode, "account", view)
}

// handleAccountDelete removes the signed-in account and everything it holds.
//
// The confirmation asks for the password, a second factor when one is set, and
// the username typed out. Deleting an account is unrecoverable and takes every
// upload with it, so it is worth being slightly annoying about.
func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if err := s.verifySensitiveAction(w, r, user); err != nil {
		s.redirectFlash(w, r, "/settings/account", flashError, err.Error())
		return
	}

	if typed := trimSpace(r.FormValue("confirm")); typed != user.Username {
		s.redirectFlash(w, r, "/settings/account", flashError,
			"Type your username exactly to confirm. Nothing has been deleted.")
		return
	}

	removed, err := s.deleteAccount(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			s.redirectFlash(w, r, "/settings/account", flashError,
				"You are the last administrator, so this account cannot be deleted. "+
					"Grant somebody else the role first.")
			return
		}
		s.log.Error("account: delete", "user", user.ID, "error", err)
		s.redirectFlash(w, r, "/settings/account", flashError, "Could not delete the account.")
		return
	}

	s.log.Info("account deleted by its owner", "user", user.ID, "files_removed", removed)

	// The session belongs to an account that no longer exists.
	s.clearSessionCookie(w, r)
	s.clearPendingCookie(w, r)

	s.redirectFlash(w, r, "/", flashNotice,
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

// handleUnlinkIdentity disconnects a provider from the signed-in account.
//
// An account made through a provider has no password anybody knows, so for it
// the last connection is the only way in. Removing that is refused rather than
// allowed to lock the account out; setting a password first lifts it.
func (s *Server) handleUnlinkIdentity(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "That sign-in connection doesn't exist.")
		return
	}

	if message := s.unlinkRefusal(r, user); message != "" {
		s.redirectFlash(w, r, "/settings/account", flashError, message)
		return
	}

	if err := s.store.UnlinkIdentity(r.Context(), id, user.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.redirectFlash(w, r, "/settings/account", flashError, "That connection does not exist.")
			return
		}
		s.log.Error("unlink identity", "user", user.ID, "identity", id, "error", err)
		s.redirectFlash(w, r, "/settings/account", flashError, "Could not disconnect it.")
		return
	}

	s.log.Info("identity disconnected", "user", user.ID, "identity", id)
	s.redirectFlash(w, r, "/settings/account", flashNotice,
		"Disconnected. You can still sign in with your password.")
}

// unlinkRefusal explains why the account may not remove a connection, or is
// empty when it may.
func (s *Server) unlinkRefusal(r *http.Request, user *models.User) string {
	set, err := s.store.PasswordSet(r.Context(), user.ID)
	if err != nil {
		s.log.Error("unlink: load password state", "user", user.ID, "error", err)
		return "Could not disconnect it."
	}
	if set {
		return ""
	}
	identities, err := s.store.IdentitiesByUser(r.Context(), user.ID)
	if err != nil {
		s.log.Error("unlink: list identities", "user", user.ID, "error", err)
		return "Could not disconnect it."
	}
	if len(identities) <= 1 {
		return "This is the only way into this account. Set a password first, then disconnect it."
	}
	return ""
}

func (s *Server) handleTimezone(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, 400, "That form couldn't be read. Please try again.")
		return
	}
	name, err := displaytime.ParseForm(r.PostForm)
	if err == nil {
		err = s.store.SetTimezone(r.Context(), currentUser(r.Context()).ID, name)
	}
	if errors.Is(err, displaytime.ErrTimezone) {
		s.accountPage(w, r, 422, &name, err.Error())
		return
	}
	if err != nil {
		s.fail(w, r, 500, "Your timezone couldn't be saved. Please try again.")
		return
	}
	http.Redirect(w, r, "/settings/account?saved=timezone#display-timezone", 303)
}
