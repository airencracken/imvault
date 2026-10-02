// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"imvault/internal/models"
	"imvault/internal/oidc"
	"imvault/internal/store"
)

// An account made through a provider has no password anybody knows. Every
// setting that asks for "your current password" would be out of its reach, so
// such an account proves itself the other way it can: a fresh sign-in at the
// provider, good for a short while and only in the session that asked.

const (
	// reauthCookie carries a sealed proof of a recent provider sign-in.
	reauthCookie = "imvault_reauth"
	// reauthWindow is how long that proof stands in for a password.
	reauthWindow = 10 * time.Minute
)

// userError is an error whose text is meant for the person who caused it, as
// opposed to a failure to be logged.
type userError string

func (e userError) Error() string { return string(e) }

// reauthProof is what the cookie holds once sealed.
type reauthProof struct {
	UserID int64 `json:"user_id"`
	// Session is the digest of the session that confirmed, so the proof is no
	// use to any other session, including a later one on the same browser.
	Session string    `json:"session"`
	At      time.Time `json:"at"`
}

// credentialState describes how an account proves itself, for the settings
// pages that have to ask.
type credentialState struct {
	// PasswordSet is whether the account has a password somebody chose.
	PasswordSet bool
	// Reauthenticated is whether a provider confirmation is in its window.
	Reauthenticated bool
	// CanReauthenticate is whether the account can confirm through a provider.
	CanReauthenticate bool
	// Next is where a confirmation returns to.
	Next string
}

// credentialsFor reports how the signed-in account proves itself.
func (s *Server) credentialsFor(r *http.Request, user *models.User, next string) credentialState {
	state := credentialState{PasswordSet: true, Next: next}
	set, err := s.store.PasswordSet(r.Context(), user.ID)
	if err != nil {
		s.log.Error("load password state", "user", user.ID, "error", err)
		return state
	}
	state.PasswordSet = set
	state.Reauthenticated = s.recentlyReauthenticated(r, user)
	if s.oidc.Enabled() {
		identities, err := s.store.IdentitiesByUser(r.Context(), user.ID)
		if err != nil {
			s.log.Error("load identities", "user", user.ID, "error", err)
		}
		state.CanReauthenticate = len(identities) > 0
	}
	return state
}

// sessionDigest is the stored form of the request's session token.
func (s *Server) sessionDigest(r *http.Request) string {
	c, err := r.Cookie(s.cookieName(r, sessionCookie))
	if err != nil || c.Value == "" {
		return ""
	}
	return hashToken(c.Value)
}

// recentlyReauthenticated reports whether this session confirmed this account
// at the provider within the window.
func (s *Server) recentlyReauthenticated(r *http.Request, user *models.User) bool {
	cookie, err := r.Cookie(reauthCookie)
	if err != nil || cookie.Value == "" || user == nil {
		return false
	}
	payload, err := s.secrets.Decrypt(cookie.Value)
	if err != nil {
		return false
	}
	var proof reauthProof
	if err := json.Unmarshal([]byte(payload), &proof); err != nil {
		return false
	}
	session := s.sessionDigest(r)
	age := time.Since(proof.At)
	return proof.UserID == user.ID && session != "" && proof.Session == session &&
		age >= 0 && age <= reauthWindow
}

// grantReauth records a confirmation for the current session.
func (s *Server) grantReauth(w http.ResponseWriter, r *http.Request, user *models.User) error {
	payload, err := json.Marshal(reauthProof{UserID: user.ID, Session: s.sessionDigest(r), At: time.Now().UTC()})
	if err != nil {
		return err
	}
	sealed, err := s.secrets.Encrypt(string(payload))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     reauthCookie,
		Value:    sealed,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secureCookies(r),
		MaxAge:   int(reauthWindow / time.Second),
	})
	return nil
}

// errNeedsReauth is what an account with no password is told when it has not
// confirmed recently.
const errNeedsReauth = userError("Confirm it is you with your sign-in provider first, then try again.")

// checkCurrentPassword accepts the account's password or, for an account that
// has none, a recent provider confirmation.
func (s *Server) checkCurrentPassword(r *http.Request, user *models.User, password string) error {
	set, err := s.store.PasswordSet(r.Context(), user.ID)
	if err != nil {
		s.log.Error("load password state", "user", user.ID, "error", err)
		return userError("Your password could not be checked. Try again.")
	}
	if !set {
		if s.recentlyReauthenticated(r, user) {
			return nil
		}
		return errNeedsReauth
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return userError("That is not your current password.")
	}
	return nil
}

// handleReauthStart sends the signed-in account to its provider to confirm.
func (s *Server) handleReauthStart(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}
	next := safeNext(r.PostFormValue("next"))
	if next == "" {
		next = "/settings/password"
	}
	if !s.oidc.Enabled() {
		s.redirectFlash(w, r, next, flashError, "This instance has no sign-in provider.")
		return
	}
	identities, err := s.store.IdentitiesByUser(r.Context(), user.ID)
	if err != nil || len(identities) == 0 {
		s.redirectFlash(w, r, next, flashError, "This account has no sign-in provider to confirm with.")
		return
	}
	s.sendToProvider(w, r, oidcAttempt{Reauth: user.ID, Next: next})
}

// finishReauth completes a confirmation: the provider must name an identity
// already linked to the account that asked, in the session that asked.
func (s *Server) finishReauth(w http.ResponseWriter, r *http.Request, attempt oidcAttempt, identity *oidc.Identity) {
	user := currentUser(r.Context())
	if user == nil || user.ID != attempt.Reauth {
		s.oidcFailed(w, r, "Your session ended during the confirmation. Sign in and try again.")
		return
	}
	linked, err := s.store.IdentityBySubject(r.Context(), s.cfg.OIDCIssuer, identity.Subject)
	if err != nil || linked.UserID != user.ID {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.log.Error("reauth: look up identity", "error", err)
		}
		s.log.Warn("reauth: provider named another identity", "user", user.ID)
		s.oidcFailed(w, r, "That sign-in is not connected to this account.")
		return
	}
	if err := s.grantReauth(w, r, user); err != nil {
		s.log.Error("reauth: seal proof", "user", user.ID, "error", err)
		s.oidcFailed(w, r, "The confirmation could not be recorded.")
		return
	}
	next := safeNext(attempt.Next)
	if next == "" {
		next = "/settings/password"
	}
	s.redirectFlash(w, r, next, flashNotice, "Confirmed. For the next ten minutes you can change settings without a password.")
}
