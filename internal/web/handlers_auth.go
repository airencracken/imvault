// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"imvault/internal/accounts"
	"imvault/internal/ids"
	"imvault/internal/invites"
	"imvault/internal/models"
	"imvault/internal/store"
)

// handleHome is the landing page; signed-in users go straight to their gallery.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if currentUser(r.Context()) != nil {
		// The feed, not your own gallery: the point of signing in is to see
		// what everybody has shared, not only what you put there.
		http.Redirect(w, r, "/recent", http.StatusSeeOther)
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

// admission is the outcome of checking whether a registration may proceed.
type admission struct {
	// Invite is the row to consume, or nil when none is needed.
	Invite *models.Invite
	// Refusal explains why the attempt may not proceed. Empty means it may.
	Refusal string
}

// admit decides whether a registration may go ahead, and against which
// invitation.
//
// An invitation always admits, whatever the general policy says. It is a
// deliberate grant by an administrator, which is what makes "close signup, then
// invite the people you want" work: a closed instance is not an unreachable
// one, and without this the only way to add somebody to a closed instance was
// to edit the database by hand.
func (s *Server) admit(ctx context.Context, code string) admission {
	policy := s.policy()
	code = strings.TrimSpace(code)

	if code == "" {
		if policy.AllowSignup && !policy.InviteOnly {
			return admission{}
		}
		if policy.InviteOnly {
			return admission{Refusal: "This instance is by invitation. Enter the code you were given."}
		}
		return admission{Refusal: "Registration is disabled on this instance."}
	}

	prefix, ok := invites.Split(code)
	if !ok {
		return admission{Refusal: "That invitation is not valid, or has already been used."}
	}

	found, err := s.store.InviteByPrefix(ctx, prefix)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return admission{Refusal: "That invitation is not valid, or has already been used."}
		}
		return admission{Refusal: "That invitation is not valid, or has already been used."}
	}

	// Looking the row up by prefix narrows the search; it does not authenticate
	// anything. Only a digest match does.
	hash, err := s.store.InviteCodeHash(ctx, prefix)
	if err != nil {
		return admission{Refusal: "That invitation is not valid, or has already been used."}
	}
	if !invites.Verify(code, hash) {
		return admission{Refusal: "That invitation is not valid, or has already been used."}
	}
	// A revoked, expired, or used-up code is refused here for a clear message;
	// the redemption enforces the same conditions atomically, which is what
	// stops two people consuming the last use at once.
	if !found.Usable(time.Now()) {
		return admission{Refusal: "That invitation is not valid, or has already been used."}
	}

	return admission{Invite: found}
}

func (s *Server) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if currentUser(r.Context()) != nil {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}

	policy := s.policy()

	// The page is shown even when registration is closed, because an invitation
	// still admits and that is where it is entered.
	view := authView{
		base:           s.base(r, "Create account"),
		Invite:         strings.TrimSpace(r.URL.Query().Get("invite")),
		InviteOnly:     policy.InviteOnly,
		RegisterClosed: !policy.AllowSignup,
	}
	s.renderPage(w, http.StatusOK, "register", view)
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
		base:           s.base(r, "Create account"),
		Username:       username,
		Email:          email,
		Invite:         strings.TrimSpace(r.FormValue("invite")),
		InviteOnly:     s.policy().InviteOnly,
		RegisterClosed: !s.policy().AllowSignup,
	}
	renderErr := func(status int, message string) {
		view.Error = message
		s.renderPage(w, status, "register", view)
	}

	// Whether this attempt may proceed at all, and against which invitation.
	// An invitation always admits, so this replaces the old plain check on the
	// signup switch.
	decision := s.admit(r.Context(), r.FormValue("invite"))
	if decision.Refusal != "" {
		renderErr(http.StatusForbidden, decision.Refusal)
		return
	}
	if err := accounts.ValidateRegistration(username, email, password); err != nil {
		renderErr(http.StatusBadRequest, err.Error())
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.log.Error("register: hash password", "error", err)
		http.Error(w, "could not create account", http.StatusInternalServerError)
		return
	}

	newcomer := store.NewUser{
		Username:     username,
		Email:        email,
		PasswordHash: string(hash),
		Role:         models.RoleMember,
		QuotaBytes:   s.cfg.DefaultQuotaBytes,
	}

	// With an invitation, the account and the consumed use are one transaction,
	// so registering a taken username does not burn the code and a use cannot
	// disappear without an account to show for it.
	var user *models.User
	if decision.Invite != nil {
		user, err = s.store.RegisterWithInvite(r.Context(), newcomer, decision.Invite.ID)
	} else {
		user, err = s.store.CreateUser(r.Context(), newcomer)
	}
	if err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			renderErr(http.StatusConflict, "That username is already taken.")
		case errors.Is(err, store.ErrInviteUnusable):
			renderErr(http.StatusForbidden,
				"That invitation is not valid, or has already been used.")
		default:
			s.log.Error("register: create user", "error", err)
			http.Error(w, "could not create account", http.StatusInternalServerError)
		}
		return
	}

	if decision.Invite != nil {
		s.log.Info("account registered with an invitation",
			"user", user.ID, "invite", decision.Invite.ID, "prefix", decision.Invite.Prefix)
	}
	s.finishRegistration(w, r, user)
}

func (s *Server) finishRegistration(w http.ResponseWriter, r *http.Request, user *models.User) {
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

	redirectNotice(w, r, "/gallery", "notice", "Welcome to imvault.")
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

// canViewVisibility reports whether a viewer may see content at a given level,
// ignoring ownership. Ownership is checked separately, because the owner can
// always see their own.
func canViewVisibility(user *models.User, visibility models.Visibility) bool {
	switch visibility {
	case models.VisibilityPublic:
		return true
	case models.VisibilityMembers:
		return user != nil
	default:
		return false
	}
}

// ownsFile reports whether the account uploaded the file.
//
// It takes no administrative bypass, on purpose. An album may widen *who* can
// add, but nobody puts somebody else's upload into an album, administrator
// included: the contributor and the uploader are the same person, and that is
// what keeps sharing from becoming a way to shuffle files around.
func ownsFile(user *models.User, f *models.File) bool {
	return user != nil && f.UserID != nil && *f.UserID == user.ID
}

// ownsAlbum reports whether the account created the album.
func ownsAlbum(user *models.User, a *models.Album) bool {
	return user != nil && a.UserID == user.ID
}

// canModerateContent reports whether the account may act on other people's
// content: look at it, and remove it.
func canModerateContent(user *models.User) bool {
	return user != nil && user.CanModerate()
}

// canChangeFile reports whether the account may change what a file *is*: its
// visibility and its tags. The owner and administrators may; a moderator may
// not, and that line matters.
//
// Publishing somebody's private upload is not a moderation action, and a
// moderator who could republish content would be an escalation rather than a
// safeguard. Removing a bad file is the moderation action, and that is
// canDeleteFile.
func canChangeFile(user *models.User, f *models.File) bool {
	return ownsFile(user, f) || (user != nil && user.IsAdmin())
}

// canDeleteFile reports whether the account may remove a file: the owner, a
// moderator, or an administrator.
func canDeleteFile(user *models.User, f *models.File) bool {
	return ownsFile(user, f) || canModerateContent(user)
}

// canViewFile reports whether the account may see the file's content.
//
// A moderator can see anything, because judging content you are not allowed to
// look at is not possible. That is a real consequence of the role, which is why
// the interface says so before granting it.
func canViewFile(user *models.User, f *models.File) bool {
	return canViewVisibility(user, f.Visibility) || canDeleteFile(user, f)
}

// canAdministerAlbum reports whether the account may change what an album is:
// its title, its description, its visibility, and who may contribute. The owner
// and administrators may; a moderator may not, for the same reason it may not
// republish a file.
func canAdministerAlbum(user *models.User, a *models.Album) bool {
	return ownsAlbum(user, a) || (user != nil && user.IsAdmin())
}

// canDeleteAlbum reports whether the account may remove an album. A moderator
// may, because an album is content; changing one is not.
func canDeleteAlbum(user *models.User, a *models.Album) bool {
	return ownsAlbum(user, a) || canModerateContent(user)
}

// canViewAlbum reports whether the account may see the album's page.
func canViewAlbum(user *models.User, a *models.Album) bool {
	return canViewVisibility(user, a.Visibility) ||
		canAdministerAlbum(user, a) || canModerateContent(user)
}

// canContributeToAlbum reports whether user may add their own files to album.
//
// The owner and administrators always can. On a shared album, any account that
// can see the album can as well. Either way only the contributor's *own* files
// may be added, which is what keeps sharing from becoming a way to shuffle
// somebody else's uploads around.
func canContributeToAlbum(user *models.User, a *models.Album) bool {
	if canAdministerAlbum(user, a) {
		return true
	}
	return user != nil && a.Access.Shared() && canViewAlbum(user, a)
}
