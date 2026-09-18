// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"imvault/internal/ids"
	"imvault/internal/models"
	"imvault/internal/store"
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

const minPasswordLen = 8

// maxPasswordLen bounds the input to bcrypt, which silently truncates beyond 72
// bytes; refusing is friendlier than quietly ignoring the tail.
const maxPasswordLen = 72

// validatePassword applies the rules every password-setting path shares.
func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLen {
		return fmt.Errorf("Password must be at least %d characters.", minPasswordLen)
	}
	if len(password) > maxPasswordLen {
		return fmt.Errorf("Password must be at most %d bytes.", maxPasswordLen)
	}
	return nil
}

// handleHome is the landing page; signed-in users go straight to their gallery.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if currentUser(r.Context()) != nil {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}

	// Show off a little of what the instance holds.
	recent, err := s.store.ListFiles(r.Context(), store.FileQuery{PublicOnly: true, Limit: 12})
	if err != nil {
		s.log.Error("home: list recent", "error", err)
	}

	s.renderPage(w, http.StatusOK, "home", homeView{
		base: s.base(r, ""),
		Grid: s.grid(r, recent, false, false, "", "No public images yet."),
	})
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if currentUser(r.Context()) != nil {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}
	s.renderPage(w, http.StatusOK, "login", authView{
		base: s.base(r, "Sign in"),
		Next: safeNext(r.URL.Query().Get("next")),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	next := safeNext(r.FormValue("next"))

	view := authView{
		base:     s.base(r, "Sign in"),
		Next:     next,
		Username: username,
	}

	// Deliberately vague error to avoid confirming which usernames exist.
	fail := func() {
		view.Error = "Incorrect username or password."
		s.renderPage(w, http.StatusUnauthorized, "login", view)
	}

	user, err := s.store.UserByUsername(r.Context(), username)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("login: lookup user", "error", err)
		}
		fail()
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		fail()
		return
	}

	if user.Disabled {
		s.log.Warn("login: disabled account", "username", user.Username, "remote", r.RemoteAddr)
		view.Error = "This account has been disabled."
		s.renderPage(w, http.StatusForbidden, "login", view)
		return
	}

	// With a second factor on, the password is only half of it: the rest is
	// proved at /login/2fa, which the pending cookie authorises.
	if user.TwoFactorRequired() {
		if err := s.startPendingLogin(r.Context(), w, r, user.ID); err != nil {
			s.log.Error("login: start pending login", "error", err)
			http.Error(w, "could not start session", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/login/2fa", http.StatusSeeOther)
		return
	}

	if err := s.startSession(r.Context(), w, r, user.ID); err != nil {
		s.log.Error("login: start session", "error", err)
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return
	}

	if next != "" {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/gallery", http.StatusSeeOther)
}

func (s *Server) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if currentUser(r.Context()) != nil {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}

	// The very first account can always be created; it becomes the admin.
	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		s.log.Error("register: count users", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	if count > 0 && !s.cfg.AllowSignup {
		s.renderPage(w, http.StatusForbidden, "register", authView{
			base: s.baseErr(r, "Registration closed",
				"Registration is disabled on this instance."),
		})
		return
	}

	s.renderPage(w, http.StatusOK, "register", authView{base: s.base(r, "Create account")})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	view := authView{
		base:     s.base(r, "Create account"),
		Username: username,
		Email:    email,
	}
	renderErr := func(status int, message string) {
		view.Error = message
		s.renderPage(w, status, "register", view)
	}

	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		s.log.Error("register: count users", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	// The first user bootstraps the instance and always becomes an admin.
	firstUser := count == 0
	if !firstUser && !s.cfg.AllowSignup {
		renderErr(http.StatusForbidden, "Registration is disabled on this instance.")
		return
	}

	switch {
	case !usernameRe.MatchString(username):
		renderErr(http.StatusBadRequest, "Username must be 3-32 characters, using letters, digits, dot, dash or underscore.")
		return
	}
	if err := validatePassword(password); err != nil {
		renderErr(http.StatusBadRequest, err.Error())
		return
	}
	switch {
	case email != "" && !validEmail(email):
		renderErr(http.StatusBadRequest, "That email address does not look valid.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.log.Error("register: hash password", "error", err)
		http.Error(w, "could not create account", http.StatusInternalServerError)
		return
	}

	user, err := s.store.CreateUser(r.Context(), store.NewUser{
		Username:     username,
		Email:        email,
		PasswordHash: string(hash),
		IsAdmin:      firstUser,
		QuotaBytes:   s.cfg.DefaultQuotaBytes,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			renderErr(http.StatusConflict, "That username is already taken.")
			return
		}
		s.log.Error("register: create user", "error", err)
		http.Error(w, "could not create account", http.StatusInternalServerError)
		return
	}

	if err := s.startSession(r.Context(), w, r, user.ID); err != nil {
		s.log.Error("register: start session", "error", err)
		http.Error(w, "account created, but sign-in failed", http.StatusInternalServerError)
		return
	}

	// Confirming the address is optional and never gates anything, so a failure
	// here must not fail the registration.
	if user.Email != "" {
		if err := s.sendVerificationEmail(r.Context(), r, user); err != nil {
			s.log.Error("register: send verification", "user", user.ID, "error", err)
		}
	}

	notice := "Welcome to imvault."
	if firstUser {
		notice = "Welcome. As the first account, you are the administrator."
	}
	redirectNotice(w, r, "/gallery", "notice", notice)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := s.store.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			s.log.Error("logout: delete session", "error", err)
		}
	}
	s.clearSessionCookie(w, r)

	if isHTMX(r) {
		hxRedirect(w, "/")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// startSession creates a session row and sets the cookie.
func (s *Server) startSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64) error {
	token := ids.Token(32)
	expires := time.Now().Add(s.cfg.SessionTTL)

	if err := s.store.CreateSession(ctx, hashToken(token), userID, expires); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.SecureCookies || isSecureRequest(r),
		Expires:  expires,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	})
	return nil
}

// safeNext only allows same-site relative redirect targets.
func safeNext(next string) string {
	if next == "" {
		return ""
	}
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return ""
	}
	return next
}

func validEmail(addr string) bool {
	parsed, err := mail.ParseAddress(addr)
	return err == nil && parsed.Address == addr && strings.Contains(addr, ".")
}

// requireOwnership reports whether user may modify file.
func canEditFile(user *models.User, f *models.File) bool {
	if user == nil {
		return false
	}
	if user.IsAdmin {
		return true
	}
	return f.UserID != nil && *f.UserID == user.ID
}

// canViewFile reports whether user may see the file's content.
func canViewFile(user *models.User, f *models.File) bool {
	if f.IsPublic {
		return true
	}
	return canEditFile(user, f)
}
