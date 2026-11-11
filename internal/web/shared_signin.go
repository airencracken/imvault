// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"fmt"
	"github.com/airencracken/comfylib/clientip"
	"imvault/internal/sharedoidc"
	"imvault/internal/suite"
	"net/http"
)

func (s *Server) registerSharedSignIn(mux *http.ServeMux) error {
	auth, err := sharedoidc.FromEnvironment("IMVAULT_", s.cfg.BaseURL, s.store.DB(), suite.AuthHooks{
		RateKey: func(r *http.Request) string { return clientip.NetworkKey(s.clientIP(r)) },
		Current: func(r *http.Request) (int64, string) {
			u := currentUser(r.Context())
			if u == nil {
				return 0, ""
			}
			binding := ""
			if c, err := r.Cookie(s.cookieName(r, sessionCookie)); err == nil {
				binding = c.Value
			}
			return u.ID, binding
		},
		CSRF:   func(r *http.Request) (string, string) { return "csrf_token", csrfToken(r.Context()) },
		SignIn: s.sharedSignIn,
	})
	if err != nil {
		return err
	}
	if auth != nil {
		auth.Register(mux)
	}
	return nil
}

func (s *Server) sharedSignIn(w http.ResponseWriter, r *http.Request, id int64) (string, error) {
	u, err := s.store.UserByID(r.Context(), id)
	if err != nil || u.Disabled {
		return "", fmt.Errorf("account unavailable")
	}
	if u.TwoFactorRequired() {
		if err = s.startPendingLogin(r.Context(), w, r, id); err != nil {
			return "", err
		}
		return "/login/2fa", nil
	}
	if err = s.startSession(r.Context(), w, r, id); err != nil {
		return "", err
	}
	return "/", nil
}
