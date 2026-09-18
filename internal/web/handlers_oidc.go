// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"imvault/internal/ids"
	"imvault/internal/models"
	"imvault/internal/oidc"
	"imvault/internal/store"
)

// oidcStateCookie carries a sign-in that is in progress.
//
// It is sealed with the instance key rather than merely signed, so the PKCE
// verifier inside it is never readable by the browser holding it.
const oidcStateCookie = "imvault_oidc"

// oidcAttemptWindow is how long a sign-in may sit at the provider before it is
// abandoned.
const oidcAttemptWindow = 10 * time.Minute

// oidcAttemptCookiePath scopes the cookie to the three routes that use it.
const oidcAttemptCookiePath = "/auth/oidc"

// oidcAttempt is what has to survive the round trip to the provider.
type oidcAttempt struct {
	State     string    `json:"state"`
	Nonce     string    `json:"nonce"`
	Verifier  string    `json:"verifier"`
	CreatedAt time.Time `json:"created_at"`
	// LinkTo is the account to attach the identity to, set when somebody
	// already signed in asked to connect a provider.
	LinkTo int64 `json:"link_to,omitempty"`
	// Identity is a verified assertion waiting for a username and, on an
	// invitation-only instance, a code.
	Identity *oidc.Identity `json:"identity,omitempty"`
	// Invite carries a code supplied before leaving for the provider.
	Invite string `json:"invite,omitempty"`
}

func (s *Server) setOIDCAttempt(w http.ResponseWriter, r *http.Request, attempt oidcAttempt) error {
	payload, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	sealed, err := s.secrets.Encrypt(string(payload))
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    sealed,
		Path:     oidcAttemptCookiePath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.SecureCookies || isSecureRequest(r),
		MaxAge:   int(oidcAttemptWindow / time.Second),
	})
	return nil
}

func (s *Server) readOIDCAttempt(r *http.Request) (oidcAttempt, bool) {
	var attempt oidcAttempt

	cookie, err := r.Cookie(oidcStateCookie)
	if err != nil || cookie.Value == "" {
		return attempt, false
	}
	payload, err := s.secrets.Decrypt(cookie.Value)
	if err != nil {
		return attempt, false
	}
	if err := json.Unmarshal([]byte(payload), &attempt); err != nil {
		return attempt, false
	}
	return attempt, true
}

func (s *Server) clearOIDCAttempt(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    "",
		Path:     oidcAttemptCookiePath,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies || isSecureRequest(r),
		MaxAge:   -1,
	})
}

// oidcRedirectURL is where the provider sends the browser back. Configuration
// requires an absolute base URL, because the redirect URI is registered with
// the provider and must match exactly.
func (s *Server) oidcRedirectURL(r *http.Request) string {
	return s.absoluteURL(r, "/auth/oidc/callback")
}

// Expired reports whether an attempt is too old to still be in progress.
func (a oidcAttempt) Expired(now time.Time) bool {
	return now.Sub(a.CreatedAt) > oidcAttemptWindow
}
func (s *Server) oidcFailed(w http.ResponseWriter, r *http.Request, message string) {
	s.renderPage(w, http.StatusBadRequest, "notfound", errorView{
		base:    s.base(r, "Sign-in failed"),
		Code:    "Sign-in failed",
		Message: message,
	})
}

// handleOIDCStart sends the browser to the provider.
func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	if !s.oidc.Enabled() {
		http.NotFound(w, r)
		return
	}

	provider, err := s.oidc.Get(r.Context())
	if err != nil {
		s.log.Error("oidc: discovery failed", "error", err)
		s.renderPage(w, http.StatusBadGateway, "notfound", errorView{
			base: s.base(r, "Sign-in unavailable"),
			Code: "Sign-in unavailable",
			Message: "The identity provider could not be reached. Try again, " +
				"or sign in with a password.",
		})
		return
	}

	started := provider.Start(s.oidcRedirectURL(r))
	attempt := oidcAttempt{
		State:     started.State,
		Nonce:     started.Nonce,
		Verifier:  started.Verifier,
		CreatedAt: started.CreatedAt,
		Invite:    strings.TrimSpace(r.URL.Query().Get("invite")),
	}

	// Connecting a provider to the account already signed in, rather than
	// signing in as whoever the provider names.
	if user := currentUser(r.Context()); user != nil && r.URL.Query().Get("connect") != "" {
		attempt.LinkTo = user.ID
	}

	if err := s.setOIDCAttempt(w, r, attempt); err != nil {
		s.log.Error("oidc: seal attempt", "error", err)
		http.Error(w, "could not start the sign-in", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, started.URL, http.StatusSeeOther)
}

// handleOIDCCallback redeems what the provider sent back.
func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if !s.oidc.Enabled() {
		http.NotFound(w, r)
		return
	}

	attempt, ok := s.readOIDCAttempt(r)
	s.clearOIDCAttempt(w, r)
	if !ok {
		s.oidcFailed(w, r, "That sign-in did not start here, or it took too long. Try again.")
		return
	}
	if attempt.Expired(time.Now().UTC()) {
		s.oidcFailed(w, r, "That sign-in took too long. Start again.")
		return
	}

	// The provider may report a refusal rather than send a code.
	if refusal := r.URL.Query().Get("error"); refusal != "" {
		s.log.Info("oidc: provider refused",
			"error", refusal, "description", r.URL.Query().Get("error_description"))
		s.oidcFailed(w, r, "The identity provider refused the sign-in.")
		return
	}

	// State is the only thing tying this callback to the attempt that started
	// it. Without it, a stranger could hand us their own authorization code.
	if r.URL.Query().Get("state") != attempt.State {
		s.log.Warn("oidc: state mismatch on callback")
		s.oidcFailed(w, r, "That sign-in did not start here. Try again.")
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		s.oidcFailed(w, r, "The identity provider returned no authorization code.")
		return
	}

	provider, err := s.oidc.Get(r.Context())
	if err != nil {
		s.log.Error("oidc: discovery failed", "error", err)
		s.oidcFailed(w, r, "The identity provider could not be reached.")
		return
	}

	identity, err := provider.Exchange(r.Context(), code, attempt.Verifier, s.oidcRedirectURL(r), attempt.Nonce)
	if err != nil {
		s.log.Warn("oidc: exchange failed", "error", err)
		s.oidcFailed(w, r, "The identity provider's answer could not be verified.")
		return
	}

	// Connecting to the account that asked for it.
	if attempt.LinkTo != 0 {
		user := currentUser(r.Context())
		if user == nil || user.ID != attempt.LinkTo {
			s.oidcFailed(w, r, "Your session ended during the sign-in. Try again from your account page.")
			return
		}
		s.connectIdentity(w, r, user, identity)
		return
	}

	// A known identity signs straight in.
	existing, err := s.store.IdentityBySubject(r.Context(), s.cfg.OIDCIssuer, identity.Subject)
	switch {
	case err == nil:
		user, err := s.store.UserByID(r.Context(), existing.UserID)
		if err != nil {
			s.log.Error("oidc: identity points at a missing account", "identity", existing.ID, "error", err)
			s.oidcFailed(w, r, "That sign-in points at an account that no longer exists.")
			return
		}
		if err := s.store.TouchIdentity(r.Context(), existing.ID); err != nil {
			s.log.Warn("oidc: touch identity", "error", err)
		}
		s.finishOIDCSignIn(w, r, user)
		return
	case !errors.Is(err, store.ErrNotFound):
		s.log.Error("oidc: look up identity", "error", err)
		s.oidcFailed(w, r, "Something went wrong on this server.")
		return
	}

	// An address is only evidence if the provider says it checked. Letting an
	// unverified address link would hand over an account on the strength of a
	// claim anybody can make to some providers.
	if identity.EmailVerified && identity.Email != "" {
		users, err := s.store.UsersByEmail(r.Context(), identity.Email)
		if err != nil {
			s.log.Error("oidc: look up email", "error", err)
			s.oidcFailed(w, r, "Something went wrong on this server.")
			return
		}

		switch len(users) {
		case 1:
			s.linkAndSignIn(w, r, users[0], identity)
			return
		case 0:
			// Nobody here uses that address, so this is a new account.
		default:
			// Addresses are not unique, so this is ambiguous. Guessing would be
			// a way to sign in as the wrong person.
			s.log.Warn("oidc: ambiguous address", "email", identity.Email, "accounts", len(users))
			s.oidcFailed(w, r, "More than one account uses that address. Sign in with "+
				"your password, then connect the provider from your account page.")
			return
		}
	}

	// An unknown person. Finishing the registration is a step of its own, under
	// the instance's own policy, so a provider is never a second front door
	// around a closed or invitation-only instance.
	attempt.Identity = identity
	if err := s.setOIDCAttempt(w, r, attempt); err != nil {
		s.log.Error("oidc: seal attempt", "error", err)
		http.Error(w, "could not continue the sign-in", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/auth/oidc/complete", http.StatusSeeOther)
}

// finishOIDCSignIn creates the session for a verified identity.
func (s *Server) finishOIDCSignIn(w http.ResponseWriter, r *http.Request, user *models.User) {
	if user.Disabled {
		s.oidcFailed(w, r, "That account is disabled.")
		return
	}

	if err := s.startSession(r.Context(), w, r, user.ID); err != nil {
		s.log.Error("oidc: start session", "error", err)
		s.oidcFailed(w, r, "Signed in, but the session could not be created.")
		return
	}

	s.log.Info("signed in with an identity provider",
		"user", user.ID, "issuer", s.cfg.OIDCIssuer)
	http.Redirect(w, r, "/recent", http.StatusSeeOther)
}

// linkAndSignIn attaches a verified identity to the account whose address it
// matches, then signs in.
func (s *Server) linkAndSignIn(w http.ResponseWriter, r *http.Request, user *models.User, identity *oidc.Identity) {
	if _, err := s.store.LinkIdentity(r.Context(), user.ID, s.cfg.OIDCIssuer, identity.Subject, identity.Email); err != nil {
		if errors.Is(err, store.ErrConflict) {
			s.oidcFailed(w, r, "That sign-in is already connected to a different account.")
			return
		}
		s.log.Error("oidc: link identity", "error", err)
		s.oidcFailed(w, r, "The sign-in could not be recorded.")
		return
	}

	s.log.Info("identity connected by verified address",
		"user", user.ID, "issuer", s.cfg.OIDCIssuer)
	s.finishOIDCSignIn(w, r, user)
}

// connectIdentity attaches a provider to an account that is already signed in.
func (s *Server) connectIdentity(w http.ResponseWriter, r *http.Request, user *models.User, identity *oidc.Identity) {
	if _, err := s.store.LinkIdentity(r.Context(), user.ID, s.cfg.OIDCIssuer, identity.Subject, identity.Email); err != nil {
		if errors.Is(err, store.ErrConflict) {
			redirectNotice(w, r, "/settings/account", "error",
				"That sign-in is already connected to a different account.")
			return
		}
		s.log.Error("oidc: connect identity", "error", err)
		redirectNotice(w, r, "/settings/account", "error", "The sign-in could not be connected.")
		return
	}

	s.log.Info("identity connected from the account page",
		"user", user.ID, "issuer", s.cfg.OIDCIssuer)
	redirectNotice(w, r, "/settings/account", "notice",
		s.oidc.Name()+" is now connected.")
}

// oidcCompleteView backs the last step of a first sign-in.
type oidcCompleteView struct {
	base
	Username string
	Email    string
	Provider string
	Invite   string
	// InviteRequired means the instance will not admit a new account without a
	// code, so the field is not optional.
	InviteRequired bool
	Error          string
}

// handleOIDCComplete asks an unknown person for a username, and for an
// invitation if the instance wants one.
func (s *Server) handleOIDCComplete(w http.ResponseWriter, r *http.Request) {
	if !s.oidc.Enabled() {
		http.NotFound(w, r)
		return
	}

	attempt, ok := s.readOIDCAttempt(r)
	if !ok || attempt.Identity == nil {
		redirectNotice(w, r, "/login", "error", "That sign-in did not finish. Try again.")
		return
	}

	view := oidcCompleteView{
		base:           s.base(r, "Finish signing in"),
		Username:       suggestUsername(attempt.Identity.Username),
		Email:          attempt.Identity.Email,
		Provider:       s.oidc.Name(),
		Invite:         attempt.Invite,
		InviteRequired: !s.admitsWithoutInvite(),
	}
	s.renderPage(w, http.StatusOK, "oidc_complete", view)
}

// handleOIDCCompleteSubmit creates the account and links the identity.
func (s *Server) handleOIDCCompleteSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.oidc.Enabled() {
		http.NotFound(w, r)
		return
	}

	attempt, ok := s.readOIDCAttempt(r)
	if !ok || attempt.Identity == nil {
		redirectNotice(w, r, "/login", "error", "That sign-in did not finish. Try again.")
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	inviteCode := strings.TrimSpace(r.FormValue("invite"))

	view := oidcCompleteView{
		base:           s.base(r, "Finish signing in"),
		Username:       username,
		Email:          attempt.Identity.Email,
		Provider:       s.oidc.Name(),
		Invite:         inviteCode,
		InviteRequired: !s.admitsWithoutInvite(),
	}
	renderErr := func(status int, message string) {
		view.Error = message
		s.renderPage(w, status, "oidc_complete", view)
	}

	if !usernameRe.MatchString(username) {
		renderErr(http.StatusBadRequest,
			"Username must be 3-32 characters, using letters, digits, dot, dash or underscore.")
		return
	}

	// The same gate an ordinary registration passes through, so a provider
	// cannot be used to walk around an invitation-only instance.
	decision := s.admit(r.Context(), false, inviteCode)
	if decision.Refusal != "" {
		renderErr(http.StatusForbidden, decision.Refusal)
		return
	}

	var inviteID *int64
	if decision.Invite != nil {
		inviteID = &decision.Invite.ID
	}

	// A random password rather than an empty hash. An account made here is
	// meant to be reached through the provider, but an empty hash would leave
	// "no password" as a state some later path could misread as "any password".
	placeholder, err := bcrypt.GenerateFromPassword([]byte(ids.Token(32)), bcrypt.DefaultCost)
	if err != nil {
		s.log.Error("oidc: placeholder password", "error", err)
		http.Error(w, "could not create the account", http.StatusInternalServerError)
		return
	}

	user, err := s.store.CreateUserWithIdentity(r.Context(), store.NewUser{
		Username:     username,
		Email:        attempt.Identity.Email,
		PasswordHash: string(placeholder),
		Role:         models.RoleMember,
		QuotaBytes:   s.cfg.DefaultQuotaBytes,
	}, s.cfg.OIDCIssuer, attempt.Identity.Subject, attempt.Identity.Email, inviteID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			renderErr(http.StatusConflict, "That username is already taken.")
		case errors.Is(err, store.ErrInviteUnusable):
			renderErr(http.StatusForbidden, "That invitation is not valid, or has already been used.")
		default:
			s.log.Error("oidc: create account", "error", err)
			http.Error(w, "could not create the account", http.StatusInternalServerError)
		}
		return
	}

	// The email the provider asserted is recorded as verified: the provider
	// checked it, and repeating our own confirmation would be noise.
	if attempt.Identity.EmailVerified && attempt.Identity.Email != "" {
		if err := s.store.SetEmail(r.Context(), user.ID, attempt.Identity.Email, true); err != nil {
			s.log.Warn("oidc: mark email verified", "user", user.ID, "error", err)
		}
	}

	s.log.Info("account created with an identity provider",
		"user", user.ID, "issuer", s.cfg.OIDCIssuer)

	s.clearOIDCAttempt(w, r)
	s.finishOIDCSignIn(w, r, user)
}

// admitsWithoutInvite reports whether an unknown person may register without a
// code under the instance's current policy.
func (s *Server) admitsWithoutInvite() bool {
	policy := s.policy()
	return policy.AllowSignup && !policy.InviteOnly
}

// suggestUsername turns whatever the provider offered into something that would
// pass the username rules, so the field starts somewhere useful.
func suggestUsername(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if at := strings.Index(raw, "@"); at >= 0 {
		raw = raw[:at]
	}

	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '+':
			b.WriteByte('-')
		}
	}

	out := strings.Trim(b.String(), ".-_")
	if len(out) < 3 {
		return ""
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}
