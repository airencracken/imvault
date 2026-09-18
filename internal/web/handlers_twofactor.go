// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"rsc.io/qr"

	"imvault/internal/models"
	"imvault/internal/store"
	"imvault/internal/tokens"
	"imvault/internal/totp"
)

const (
	// pendingCookie carries a completed password check through to the code
	// prompt.
	pendingCookie = "imvault_pending"
	// pendingTTL is how long somebody has to produce a code, which is long
	// enough to find a phone and short enough not to linger.
	pendingTTL = 5 * time.Minute
	// recoveryAlphabet omits the characters people misread: I, L, O, U, 0, 1.
	recoveryAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"
	// recoveryCodeLength is the length of one code, before grouping.
	recoveryCodeLength = 10
)

// --- the settings page ------------------------------------------------------

// twoFactorView backs the two-factor settings page.
type twoFactorView struct {
	base
	Status *store.AccountStatus
	// Pending holds the enrolment details between starting and confirming it.
	Pending *pendingEnrolment
	// RecoveryCodes is populated exactly once, when codes are generated.
	RecoveryCodes []string
	Error         string
	Notice        string
}

type pendingEnrolment struct {
	Secret       string
	SecretSpaced string
}

// handleTwoFactorPage shows the second-factor status and controls.
func (s *Server) handleTwoFactorPage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	status, err := s.store.StatusFor(r.Context(), user.ID)
	if err != nil {
		s.log.Error("two factor: load status", "user", user.ID, "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	view := twoFactorView{
		Status: status,
		Error:  strings.TrimSpace(r.URL.Query().Get("error")),
		Notice: strings.TrimSpace(r.URL.Query().Get("notice")),
	}
	view.base = s.base(r, "Two-factor authentication")

	// A secret that exists but is not enabled means enrolment is under way, and
	// the page should still be showing the QR code after a reload.
	if status.User.TOTPSecret != "" && !status.User.TOTPEnabled {
		pending, err := s.pendingEnrolment(r.Context(), status.User)
		if err != nil {
			s.log.Error("two factor: prepare enrolment", "user", user.ID, "error", err)
			view.Error = "Could not prepare the setup code. Try again."
		} else {
			view.Pending = pending
		}
	}

	s.renderPage(w, http.StatusOK, "two_factor", view)
}

// pendingEnrolment decrypts the half-finished secret.
func (s *Server) pendingEnrolment(ctx context.Context, user *models.User) (*pendingEnrolment, error) {
	secret, err := s.secrets.Decrypt(user.TOTPSecret)
	if err != nil {
		return nil, err
	}

	return &pendingEnrolment{
		Secret:       secret,
		SecretSpaced: totp.GroupSpaced(secret),
	}, nil
}

// handleTwoFactorQR renders the enrolment QR code.
//
// It is an endpoint rather than a data: URI in the page because html/template
// refuses data: in a src attribute, rewriting it to a failsafe and leaving the
// image broken. It only answers while enrolment is pending, so it cannot be
// used to read a secret back afterwards.
func (s *Server) handleTwoFactorQR(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if user.TOTPEnabled || user.TOTPSecret == "" {
		http.NotFound(w, r)
		return
	}

	secret, err := s.secrets.Decrypt(user.TOTPSecret)
	if err != nil {
		s.log.Error("two factor: decrypt for QR", "user", user.ID, "error", err)
		http.Error(w, "could not read the secret", http.StatusInternalServerError)
		return
	}

	code, err := qr.Encode(totp.ProvisioningURI(s.cfg.TOTPIssuer, user.Username, secret), qr.M)
	if err != nil {
		s.log.Error("two factor: encode QR", "user", user.ID, "error", err)
		http.Error(w, "could not render the code", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	// It is specific to the signed-in account and to a pending enrolment.
	w.Header().Set("Cache-Control", "no-store")
	w.Write(code.PNG())
}

// handleTwoFactorBegin starts enrolment by storing a fresh secret.
func (s *Server) handleTwoFactorBegin(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if user.TOTPEnabled {
		redirectNotice(w, r, "/settings/2fa", "error",
			"Two-factor authentication is already on. Turn it off first to set it up again.")
		return
	}

	secret, err := totp.GenerateSecret()
	if err != nil {
		s.log.Error("two factor: generate secret", "error", err)
		redirectNotice(w, r, "/settings/2fa", "error", "Could not generate a secret.")
		return
	}

	encrypted, err := s.secrets.Encrypt(secret)
	if err != nil {
		s.log.Error("two factor: encrypt secret", "error", err)
		redirectNotice(w, r, "/settings/2fa", "error", "Could not store the secret.")
		return
	}

	if err := s.store.BeginTOTP(r.Context(), user.ID, encrypted); err != nil {
		s.log.Error("two factor: begin", "user", user.ID, "error", err)
		redirectNotice(w, r, "/settings/2fa", "error", "Could not start setup.")
		return
	}

	http.Redirect(w, r, "/settings/2fa", http.StatusSeeOther)
}

// handleTwoFactorConfirm finishes enrolment once a code has been proved.
func (s *Server) handleTwoFactorConfirm(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	if user.TOTPEnabled {
		redirectNotice(w, r, "/settings/2fa", "notice", "Two-factor authentication is already on.")
		return
	}
	if user.TOTPSecret == "" {
		redirectNotice(w, r, "/settings/2fa", "error", "Start setup before confirming a code.")
		return
	}

	secret, err := s.secrets.Decrypt(user.TOTPSecret)
	if err != nil {
		s.log.Error("two factor: decrypt pending secret", "user", user.ID, "error", err)
		redirectNotice(w, r, "/settings/2fa", "error", "Could not read the pending secret.")
		return
	}

	if _, ok := totp.Match(secret, r.FormValue("code"), time.Now()); !ok {
		redirectNotice(w, r, "/settings/2fa", "error",
			"That code did not match. Check the clock on your device and try the current code.")
		return
	}

	if err := s.store.EnableTOTP(r.Context(), user.ID); err != nil {
		s.log.Error("two factor: enable", "user", user.ID, "error", err)
		redirectNotice(w, r, "/settings/2fa", "error", "Could not enable two-factor authentication.")
		return
	}

	codes, err := s.issueRecoveryCodes(r.Context(), user.ID)
	if err != nil {
		s.log.Error("two factor: issue recovery codes", "user", user.ID, "error", err)
		redirectNotice(w, r, "/settings/2fa", "notice",
			"Two-factor authentication is on, but the recovery codes could not be generated. Generate them again from this page.")
		return
	}

	s.log.Info("two-factor authentication enabled", "user", user.ID)
	s.renderTwoFactorPage(w, r, codes, "", "Two-factor authentication is on. Save your recovery codes now.")
}

// handleTwoFactorDisable turns the second factor off.
//
// It takes a password and a code rather than the password alone: removing a
// second factor is exactly what somebody who has the password and a borrowed
// session would want to do.
func (s *Server) handleTwoFactorDisable(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if err := s.verifySensitiveAction(w, r, user); err != nil {
		redirectNotice(w, r, "/settings/2fa", "error", err.Error())
		return
	}

	if err := s.store.DisableTOTP(r.Context(), user.ID); err != nil {
		s.log.Error("two factor: disable", "user", user.ID, "error", err)
		redirectNotice(w, r, "/settings/2fa", "error", "Could not disable two-factor authentication.")
		return
	}

	s.log.Info("two-factor authentication disabled", "user", user.ID)
	redirectNotice(w, r, "/settings/2fa", "notice",
		"Two-factor authentication is off, and the recovery codes have been discarded.")
}

// handleTwoFactorRecovery issues a fresh set of recovery codes.
func (s *Server) handleTwoFactorRecovery(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if !user.TOTPEnabled {
		redirectNotice(w, r, "/settings/2fa", "error", "Two-factor authentication is not on.")
		return
	}

	if err := s.verifySensitiveAction(w, r, user); err != nil {
		redirectNotice(w, r, "/settings/2fa", "error", err.Error())
		return
	}

	codes, err := s.issueRecoveryCodes(r.Context(), user.ID)
	if err != nil {
		s.log.Error("two factor: regenerate recovery codes", "user", user.ID, "error", err)
		redirectNotice(w, r, "/settings/2fa", "error", "Could not generate new recovery codes.")
		return
	}

	s.renderTwoFactorPage(w, r, codes, "", "Here are your new recovery codes. The previous set no longer works.")
}

// renderTwoFactorPage renders the settings page carrying a one-time value.
//
// The page is rendered rather than redirected to, because the codes exist in
// exactly one response and a redirect would drop them.
func (s *Server) renderTwoFactorPage(w http.ResponseWriter, r *http.Request, codes []string, errMsg, notice string) {
	status, err := s.store.StatusFor(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		s.log.Error("two factor: reload status", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	view := twoFactorView{
		Status:        status,
		RecoveryCodes: codes,
		Error:         errMsg,
		Notice:        notice,
	}
	view.base = s.base(r, "Two-factor authentication")

	s.renderPage(w, http.StatusOK, "two_factor", view)
}

// issueRecoveryCodes generates, stores and returns a fresh set. The plaintext
// is returned once and never stored.
func (s *Server) issueRecoveryCodes(ctx context.Context, userID int64) ([]string, error) {
	codes := make([]string, 0, store.RecoveryCodeCount)
	hashes := make([]string, 0, store.RecoveryCodeCount)

	for i := 0; i < store.RecoveryCodeCount; i++ {
		code, err := generateRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
		hashes = append(hashes, store.HashRecoveryCode(code))
	}

	if err := s.store.ReplaceRecoveryCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

// generateRecoveryCode returns one grouped code, for example "K7QP2-MX4RT".
func generateRecoveryCode() (string, error) {
	buf := make([]byte, recoveryCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("recovery code: %w", err)
	}

	var b strings.Builder
	for i, value := range buf {
		if i == recoveryCodeLength/2 {
			b.WriteByte('-')
		}
		b.WriteByte(recoveryAlphabet[int(value)%len(recoveryAlphabet)])
	}
	return b.String(), nil
}

// looksLikeAuthenticatorCode reports whether the input has the shape of a
// six-digit code. Anything else is treated as a recovery code, which is longer
// and carries a separator, so the two can never be confused.
func looksLikeAuthenticatorCode(code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != totp.Digits {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// verifySensitiveAction re-checks the password and, when a second factor is on,
// a code from it. It is what guards both disabling two-factor and deleting an
// account.
func (s *Server) verifySensitiveAction(w http.ResponseWriter, r *http.Request, user *models.User) error {
	if err := r.ParseForm(); err != nil {
		return errors.New("That request could not be read.")
	}

	password := r.FormValue("password")
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return errors.New("That is not your current password.")
	}

	if !user.TOTPEnabled {
		return nil
	}

	code := strings.TrimSpace(r.FormValue("code"))
	if code == "" {
		return errors.New("Enter a code from your authenticator app, or a recovery code.")
	}

	if err := s.checkSecondFactor(r.Context(), user, code); err != nil {
		return err
	}
	return nil
}

// checkSecondFactor accepts either a current authenticator code or an unused
// recovery code.
func (s *Server) checkSecondFactor(ctx context.Context, user *models.User, code string) error {
	if !looksLikeAuthenticatorCode(code) {
		used, err := s.store.ConsumeRecoveryCode(ctx, user.ID, store.HashRecoveryCode(code))
		if err != nil {
			return errors.New("That code could not be checked.")
		}
		if !used {
			return errors.New("That recovery code is not valid, or has already been used.")
		}
		return nil
	}

	secret, err := s.secrets.Decrypt(user.TOTPSecret)
	if err != nil {
		return errors.New("Your authenticator secret could not be read.")
	}

	step, ok := totp.Match(secret, code, time.Now())
	if !ok {
		return errors.New("That code did not match. Try the current one.")
	}

	// Refuse a code that has already been accepted: otherwise one seen over a
	// shoulder stays usable for the rest of its window.
	accepted, err := s.store.AcceptTOTPStep(ctx, user.ID, step)
	if err != nil {
		return errors.New("That code could not be checked.")
	}
	if !accepted {
		return errors.New("That code has already been used. Wait for the next one.")
	}
	return nil
}

// --- the sign-in second step -------------------------------------------------

// twoFactorPromptView backs the code prompt shown after a correct password.
type twoFactorPromptView struct {
	base
	Username string
	Error    string
}

// startPendingLogin records that the password was accepted, and hands the
// browser a short-lived token to prove it at the code prompt.
func (s *Server) startPendingLogin(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64) error {
	token, hash := tokens.New()

	// Only the newest attempt should be live, so a second sign-in invalidates
	// the first.
	if err := s.store.DeleteAuthTokensForUser(ctx, userID, store.TokenLoginSecondFactor); err != nil {
		return err
	}
	if err := s.store.CreateAuthToken(ctx, userID, store.TokenLoginSecondFactor, hash,
		time.Now().Add(pendingTTL)); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     pendingCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.SecureCookies || isSecureRequest(r),
		MaxAge:   int(pendingTTL.Seconds()),
	})
	return nil
}

// pendingLoginUser resolves the half-finished sign-in.
func (s *Server) pendingLoginUser(w http.ResponseWriter, r *http.Request) (*models.User, bool) {
	cookie, err := r.Cookie(pendingCookie)
	if err != nil || cookie.Value == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil, false
	}

	user, err := s.store.AuthTokenValid(r.Context(), tokens.Hash(cookie.Value),
		store.TokenLoginSecondFactor, time.Now())
	if err != nil {
		// Expired or already spent: back to the start rather than a dead end.
		s.clearPendingCookie(w, r)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil, false
	}
	return user, true
}

func (s *Server) clearPendingCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     pendingCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.SecureCookies || isSecureRequest(r),
	})
}

// handleLoginTwoFactorPage shows the code prompt.
func (s *Server) handleLoginTwoFactorPage(w http.ResponseWriter, r *http.Request) {
	user, ok := s.pendingLoginUser(w, r)
	if !ok {
		return
	}

	s.renderPage(w, http.StatusOK, "login_two_factor", twoFactorPromptView{
		base:     s.base(r, "Two-factor authentication"),
		Username: user.Username,
	})
}

// handleLoginTwoFactor completes sign-in once a code has been proved.
func (s *Server) handleLoginTwoFactor(w http.ResponseWriter, r *http.Request) {
	user, ok := s.pendingLoginUser(w, r)
	if !ok {
		return
	}

	renderErr := func(status int, message string) {
		s.renderPage(w, status, "login_two_factor", twoFactorPromptView{
			base:     s.base(r, "Two-factor authentication"),
			Username: user.Username,
			Error:    message,
		})
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	if user.Disabled {
		s.clearPendingCookie(w, r)
		renderErr(http.StatusForbidden, "This account has been disabled.")
		return
	}

	if err := s.checkSecondFactor(r.Context(), user, r.FormValue("code")); err != nil {
		s.log.Warn("second factor rejected", "user", user.ID, "remote", r.RemoteAddr)
		renderErr(http.StatusUnauthorized, err.Error())
		return
	}

	// The pending token is spent only on success, so a mistyped code can be
	// retried without starting over at the password.
	if cookie, err := r.Cookie(pendingCookie); err == nil && cookie.Value != "" {
		if err := s.store.DeleteAuthToken(r.Context(), tokens.Hash(cookie.Value),
			store.TokenLoginSecondFactor); err != nil {
			s.log.Error("second factor: spend pending token", "user", user.ID, "error", err)
		}
	}
	s.clearPendingCookie(w, r)

	if err := s.startSession(r.Context(), w, r, user.ID); err != nil {
		s.log.Error("second factor: start session", "user", user.ID, "error", err)
		renderErr(http.StatusInternalServerError, "Could not start a session.")
		return
	}

	s.log.Info("signed in with a second factor", "user", user.ID)
	http.Redirect(w, r, "/gallery", http.StatusSeeOther)
}

// --- administration ----------------------------------------------------------

// handleAdminClearTwoFactor removes an account's second factor.
//
// This is the way back in for somebody who has lost their authenticator and
// their recovery codes. It deliberately needs no code from the account itself,
// which is why it is administrator-only.
func (s *Server) handleAdminClearTwoFactor(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.adminTargetUser(w, r)
	if !ok {
		return
	}

	target, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		s.renderAdminRow(w, "admin_user_row", nil, adminNotice{Text: "No such account.", Error: true})
		return
	}

	if !target.TOTPEnabled {
		s.adminRespond(w, r, target, adminNotice{},
			target.Username+" does not have two-factor authentication on.", "/admin/users")
		return
	}

	if err := s.store.DisableTOTP(r.Context(), userID); err != nil {
		s.log.Error("admin: clear two factor", "user", userID, "error", err)
		s.adminRespond(w, r, target, adminNotice{},
			"Could not clear two-factor authentication.", "/admin/users")
		return
	}

	s.log.Info("administrator cleared two-factor authentication",
		"actor", currentUser(r.Context()).ID, "user", userID)

	updated := s.reloadAdminUser(r, userID)
	s.adminRespond(w, r, updated, adminNotice{Text: "Cleared two-factor authentication for " + target.Username + "."}, "", "/admin/users")
}
