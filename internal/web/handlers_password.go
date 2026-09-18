// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"imvault/internal/mail"
	"imvault/internal/models"
	"imvault/internal/store"
	"imvault/internal/tokens"
)

// resetPath and verifyPath are the URL shapes the emailed links use.
const (
	resetPath  = "/reset/"
	verifyPath = "/verify/"
)

// handleForgotPage shows the reset request form.
func (s *Server) handleForgotPage(w http.ResponseWriter, r *http.Request) {
	if currentUser(r.Context()) != nil {
		http.Redirect(w, r, "/settings/password", http.StatusSeeOther)
		return
	}

	s.renderPage(w, http.StatusOK, "forgot", forgotView{
		base:        s.base(r, "Reset your password"),
		MailEnabled: s.mail.Enabled(),
	})
}

// handleForgot issues a reset link.
//
// The response is identical whether or not the account exists, so the endpoint
// cannot be used to discover who has an account here.
func (s *Server) handleForgot(w http.ResponseWriter, r *http.Request) {
	if !s.mail.Enabled() {
		http.Redirect(w, r, "/forgot", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	identifier := strings.TrimSpace(r.FormValue("identifier"))

	if user := s.lookupAccount(r.Context(), identifier); user != nil {
		if err := s.issueReset(r.Context(), r, user, "requested a reset"); err != nil {
			s.log.Error("forgot: issue reset", "user", user.ID, "error", err)
		}
	} else {
		s.log.Info("forgot: no such account", "identifier", identifier, "remote", r.RemoteAddr)
	}

	s.renderPage(w, http.StatusOK, "forgot", forgotView{
		base:        s.base(r, "Check your email"),
		MailEnabled: true,
		Submitted:   true,
	})
}

// lookupAccount finds an account by username or email address.
func (s *Server) lookupAccount(ctx context.Context, identifier string) *models.User {
	if identifier == "" {
		return nil
	}
	if user, err := s.store.UserByUsername(ctx, identifier); err == nil {
		return user
	}
	if user, err := s.store.UserByEmail(ctx, identifier); err == nil {
		return user
	}
	return nil
}

// issueReset mints a single-use reset token and, when the account has an email
// address, sends the link to it. The reason is only used for logging.
func (s *Server) issueReset(ctx context.Context, r *http.Request, user *models.User, reason string) error {
	if user.Email == "" {
		s.log.Warn("reset requested for an account with no email address",
			"user", user.ID, "reason", reason)
		return nil
	}

	// Only the newest link should work.
	if err := s.store.DeleteAuthTokensForUser(ctx, user.ID, store.TokenPasswordReset); err != nil {
		return err
	}

	token, hash := tokens.New()
	expires := time.Now().UTC().Add(s.cfg.PasswordResetTTL)

	if err := s.store.CreateAuthToken(ctx, user.ID, store.TokenPasswordReset, hash, expires); err != nil {
		return err
	}

	link := s.absoluteURL(r, resetPath+token)
	return s.mail.Send(ctx, mail.Message{
		To:      user.Email,
		Subject: "Reset your imvault password",
		Body:    resetEmailBody(user.Username, link, s.cfg.PasswordResetTTL),
	})
}

func resetEmailBody(username, link string, ttl time.Duration) string {
	return fmt.Sprintf(`Hello %s,

Somebody asked to reset the password for your imvault account. If that was you,
open this link to choose a new one:

%s

The link can only be used once and expires in %s.

If you did not ask for this, you can ignore this message: your password has not
changed.
`, username, link, humanDuration(ttl))
}

// humanDuration renders a coarse duration for prose.
func humanDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	case d >= time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	default:
		minutes := int(d.Minutes())
		if minutes <= 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}
}

// handleResetPage shows the new-password form for a valid token.
func (s *Server) handleResetPage(w http.ResponseWriter, r *http.Request) {
	user, ok := s.resetTokenUser(w, r)
	if !ok {
		return
	}

	s.renderPage(w, http.StatusOK, "reset", resetView{
		base:     s.base(r, "Choose a new password"),
		Token:    r.PathValue("token"),
		Username: user.Username,
	})
}

// handleReset applies a new password.
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	tokenValue := r.PathValue("token")
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	renderErr := func(status int, message string) {
		s.renderPage(w, status, "reset", resetView{
			base:  s.base(r, "Choose a new password"),
			Token: tokenValue,
			Error: message,
		})
	}

	if err := validatePassword(password); err != nil {
		renderErr(http.StatusBadRequest, err.Error())
		return
	}
	if password != confirm {
		renderErr(http.StatusBadRequest, "The two passwords do not match.")
		return
	}

	// Redeeming the token here, rather than on the GET, means a double-loaded
	// form still works but a token is only ever spent once.
	user, err := s.store.ConsumeAuthToken(r.Context(), tokens.Hash(tokenValue),
		store.TokenPasswordReset, time.Now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			renderErr(http.StatusBadRequest, "That reset link is invalid or has expired. Request a new one.")
			return
		}
		s.log.Error("reset: consume token", "error", err)
		http.Error(w, "could not reset the password", http.StatusInternalServerError)
		return
	}

	if err := s.applyNewPassword(r.Context(), w, r, user, password); err != nil {
		s.log.Error("reset: apply password", "user", user.ID, "error", err)
		http.Error(w, "could not reset the password", http.StatusInternalServerError)
		return
	}

	redirectNotice(w, r, "/gallery", "notice", "Your password has been changed and you are signed in.")
}

// handleChangeEmail sets or replaces the signed-in account's email address,
// re-verifying it when mail is available.
func (s *Server) handleChangeEmail(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	renderErr := func(status int, message string) {
		s.renderPage(w, status, "password", passwordView{
			base:          s.base(r, "Password"),
			User:          user,
			MailEnabled:   s.mail.Enabled(),
			HasEmail:      user.Email != "",
			VerifyPending: user.Email != "" && !user.EmailVerified,
			Error:         message,
		})
	}

	// Changing where password resets are sent is sensitive enough to warrant
	// proving you know the current password.
	current := r.FormValue("current_password")
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(current)); err != nil {
		renderErr(http.StatusForbidden, "That is not your current password.")
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	if email != "" && !validEmail(email) {
		renderErr(http.StatusBadRequest, "That email address does not look valid.")
		return
	}

	if err := s.store.SetEmail(r.Context(), user.ID, email, false); err != nil {
		s.log.Error("change email", "user", user.ID, "error", err)
		http.Error(w, "could not update the address", http.StatusInternalServerError)
		return
	}

	message := "Your email address has been removed."
	if email != "" {
		message = "Your email address has been updated."
		// A fresh address starts unverified, and only proves itself when the
		// link in the message is followed.
		user.Email = email
		user.EmailVerified = false
		if err := s.sendVerificationEmail(r.Context(), r, user); err != nil {
			s.log.Error("change email: send verification", "user", user.ID, "error", err)
			message = "Your email address has been updated, but the confirmation message could not be sent."
		} else if s.mail.Enabled() {
			message = "Your email address has been updated. Check your inbox for a confirmation link."
		}
	}

	redirectNotice(w, r, "/settings/password", "notice", message)
}

// handleVerifyEmail confirms an address.
func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	user, err := s.store.ConsumeAuthToken(r.Context(), tokens.Hash(r.PathValue("token")),
		store.TokenEmailVerify, time.Now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r, "That confirmation link is invalid or has expired.")
			return
		}
		s.log.Error("verify email: consume token", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	if err := s.store.SetEmail(r.Context(), user.ID, user.Email, true); err != nil {
		s.log.Error("verify email: set verified", "user", user.ID, "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	redirectNotice(w, r, "/settings/password", "notice", "Thanks — your email address is confirmed.")
}

// handleChangePasswordPage shows the change-password form for a signed-in user.
func (s *Server) handleChangePasswordPage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	s.renderPage(w, http.StatusOK, "password", passwordView{
		base:          s.base(r, "Password"),
		User:          user,
		MailEnabled:   s.mail.Enabled(),
		HasEmail:      user.Email != "",
		VerifyPending: user.Email != "" && !user.EmailVerified,
	})
}

// handleChangePassword changes the password of the signed-in account.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	current := r.FormValue("current_password")
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	renderErr := func(status int, message string) {
		s.renderPage(w, status, "password", passwordView{
			base:          s.base(r, "Password"),
			User:          user,
			MailEnabled:   s.mail.Enabled(),
			HasEmail:      user.Email != "",
			VerifyPending: user.Email != "" && !user.EmailVerified,
			Error:         message,
		})
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(current)); err != nil {
		renderErr(http.StatusForbidden, "That is not your current password.")
		return
	}
	if err := validatePassword(password); err != nil {
		renderErr(http.StatusBadRequest, err.Error())
		return
	}
	if password != confirm {
		renderErr(http.StatusBadRequest, "The two passwords do not match.")
		return
	}

	if err := s.applyNewPassword(r.Context(), w, r, user, password); err != nil {
		s.log.Error("change password", "user", user.ID, "error", err)
		http.Error(w, "could not change the password", http.StatusInternalServerError)
		return
	}

	redirectNotice(w, r, "/gallery", "notice",
		"Your password has been changed. Other devices have been signed out.")
}

// applyNewPassword sets a password, signs every other device out, and starts a
// fresh session for the caller.
func (s *Server) applyNewPassword(ctx context.Context, w http.ResponseWriter, r *http.Request, user *models.User, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := s.store.SetPassword(ctx, user.ID, string(hash)); err != nil {
		return err
	}

	// A password change invalidates anything that was already signed in: that is
	// the point of changing it after a compromise.
	if err := s.store.DeleteSessionsForUser(ctx, user.ID); err != nil {
		return err
	}

	// API keys are left alone: revoking a script's credentials on an ordinary
	// password change would be surprising.
	return s.startSession(ctx, w, r, user.ID)
}

// resetTokenUser resolves a reset token without spending it.
func (s *Server) resetTokenUser(w http.ResponseWriter, r *http.Request) (*models.User, bool) {
	user, err := s.store.AuthTokenValid(r.Context(), tokens.Hash(r.PathValue("token")),
		store.TokenPasswordReset, time.Now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderPage(w, http.StatusBadRequest, "reset", resetView{
				base:  s.base(r, "Reset link"),
				Error: "That reset link is invalid or has expired. Request a new one.",
			})
			return nil, false
		}
		s.log.Error("reset page: lookup token", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return nil, false
	}
	return user, true
}

// sendVerificationEmail issues and sends an email confirmation link.
func (s *Server) sendVerificationEmail(ctx context.Context, r *http.Request, user *models.User) error {
	if !s.mail.Enabled() || user.Email == "" {
		return nil
	}

	if err := s.store.DeleteAuthTokensForUser(ctx, user.ID, store.TokenEmailVerify); err != nil {
		return err
	}

	token, hash := tokens.New()
	expires := time.Now().UTC().Add(s.cfg.EmailVerifyTTL)
	if err := s.store.CreateAuthToken(ctx, user.ID, store.TokenEmailVerify, hash, expires); err != nil {
		return err
	}

	link := s.absoluteURL(r, verifyPath+token)
	return s.mail.Send(ctx, mail.Message{
		To:      user.Email,
		Subject: "Confirm your email address",
		Body: fmt.Sprintf(`Hello %s,

Open this link to confirm this address for your imvault account:

%s

If you did not create an account, you can ignore this message.
`, user.Username, link),
	})
}
