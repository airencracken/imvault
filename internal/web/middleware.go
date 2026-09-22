// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"imvault/internal/apikeys"
	"imvault/internal/ids"
	"imvault/internal/store"
)

// statusRecorder captures the response status for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// recoverMW converts panics into 500s and keeps the process alive.
func (s *Server) recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec) // net/http uses this internally to abort.
				}
				s.log.Error("panic serving request",
					"method", r.Method,
					"path", r.URL.Path,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// logMW records one line per request.
func (s *Server) logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}

		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		}

		s.log.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", rec.bytes,
			"duration", time.Since(start).Round(time.Millisecond).String(),
			"remote", r.RemoteAddr,
		)
	})
}

// sessionMW resolves the session cookie into a user on the request context.
func (s *Server) sessionMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}

		user, err := s.store.UserBySession(r.Context(), hashToken(c.Value), time.Now())
		if err != nil || user.Disabled {
			// Stale, expired or forged cookie, or an account that has since
			// been disabled: clear it and continue anonymously.
			s.clearSessionCookie(w, r)
			next.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
	})
}

// csrfMW implements double-submit-cookie CSRF protection. The token lives in a
// readable cookie; mutating requests must echo it via header or form field.
func (s *Server) csrfMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) >= 32 {
			token = c.Value
		}
		if token == "" {
			token = ids.Token(32)
			http.SetCookie(w, &http.Cookie{
				Name:     csrfCookie,
				Value:    token,
				Path:     "/",
				SameSite: http.SameSiteLaxMode,
				Secure:   s.cfg.SecureCookies || isSecureRequest(r),
				HttpOnly: false, // the page must be able to read it
				MaxAge:   30 * 24 * 60 * 60,
			})
		}

		if isMutating(r.Method) && !isAPIKeyAuth(r.Context()) && !isAPIPath(r) {
			provided := r.Header.Get(csrfHeader)
			if provided == "" {
				// Limits and upload slots are installed before any form parser.
				// Do not use FormValue alone: it hides body-limit errors.
				var err error
				if isFormEncoded(r) {
					err = r.ParseForm()
				} else {
					err = r.ParseMultipartForm(multipartMemory)
				}
				if err != nil && !errors.Is(err, http.ErrNotMultipart) {
					status := http.StatusBadRequest
					var tooLarge *http.MaxBytesError
					if errors.As(err, &tooLarge) {
						status = http.StatusRequestEntityTooLarge
					}
					http.Error(w, "could not read form", status)
					return
				}
				provided = r.FormValue(csrfField)
			}
			if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				s.log.Warn("csrf rejection", "method", r.Method, "path", r.URL.Path)
				http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r.WithContext(withCSRF(r.Context(), token)))
	})
}

func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

// isAPIPath reports whether the request targets the bearer-token API, which is
// never authenticated by a cookie and so is not exposed to CSRF.
func isAPIPath(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/")
}

// requireUser wraps a handler so only signed-in users reach it.
func (s *Server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r.Context()) == nil {
			if isHTMX(r) {
				hxRedirect(w, "/login")
				return
			}
			http.Redirect(w, r, "/login?next="+urlQueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// apiAuthMW resolves a bearer token on the Authorization or X-API-Key header
// into a user.
//
// An unrecognised or malformed key is not rejected here: the request simply
// proceeds unauthenticated, and the requireAPIKey wrapper produces the 401.
func (s *Server) apiAuthMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Keys may use the API and download media, but cannot authenticate
		// account settings or administrator operations.
		if !isAPIPath(r) && !isMediaDownload(r) {
			next.ServeHTTP(w, r)
			return
		}
		presented := apiKeyFromRequest(r)
		if presented == "" {
			next.ServeHTTP(w, r)
			return
		}

		prefix, ok := apikeys.Split(presented)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		key, err := s.store.APIKeyByPrefix(r.Context(), prefix)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.log.Error("api auth: lookup key", "error", err)
			}
			next.ServeHTTP(w, r)
			return
		}

		if !apikeys.Verify(presented, key.KeyHash) {
			s.log.Warn("api auth: key mismatch", "prefix", prefix, "remote", r.RemoteAddr)
			next.ServeHTTP(w, r)
			return
		}
		if key.Expired(time.Now()) {
			s.log.Warn("api auth: expired key", "prefix", prefix)
			next.ServeHTTP(w, r)
			return
		}

		user, err := s.store.UserByID(r.Context(), key.UserID)
		if err != nil {
			s.log.Error("api auth: load owner", "key", key.ID, "error", err)
			next.ServeHTTP(w, r)
			return
		}
		if user.Disabled {
			s.log.Warn("api auth: disabled account", "user", user.ID, "prefix", prefix)
			next.ServeHTTP(w, r)
			return
		}

		if err := s.store.TouchAPIKey(r.Context(), key.ID, time.Now()); err != nil {
			s.log.Error("api auth: touch key", "id", key.ID, "error", err)
		}

		ctx := withAPIKeyAuth(withUser(r.Context(), user))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isMediaDownload(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "f" || parts[1] == "" {
		return false
	}
	switch parts[2] {
	case "raw", "thumb", "preview":
		return true
	default:
		return false
	}
}

// apiKeyFromRequest extracts a bearer token from either accepted header.
func apiKeyFromRequest(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return strings.TrimSpace(token)
		}
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}

// requireAPIKey guards the programmatic endpoints.
func (s *Server) requireAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAPIKeyAuth(r.Context()) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="imvault"`)
			writeAPIError(w, http.StatusUnauthorized, "a valid API key is required")
			return
		}
		next(w, r)
	}
}

// rateLimitUploads wraps an upload handler, rejecting requests that exceed the
// caller's budget.
//
// The budget is keyed by account when signed in and by client address
// otherwise, which is what protects the anonymous upload path.
func (s *Server) rateLimitUploads(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.uploads.Enabled() {
			next(w, r)
			return
		}

		key := s.rateLimitKey(r)
		ok, retryAfter := s.uploads.Allow(key)
		if ok {
			next(w, r)
			return
		}

		seconds := int(retryAfter.Seconds()) + 1
		s.log.Warn("upload rate limited",
			"key", key,
			"retry_after_s", seconds,
			"remote", r.RemoteAddr,
		)

		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		w.Header().Set("X-RateLimit-Limit", strconv.FormatFloat(s.cfg.UploadRatePerHour, 'f', -1, 64))

		if isAPIPath(r) || isHTMX(r) {
			writeAPIError(w, http.StatusTooManyRequests,
				"too many uploads; try again in "+strconv.Itoa(seconds)+"s")
			return
		}

		s.renderPage(w, http.StatusTooManyRequests, "notfound", errorView{
			base:    s.base(r, "Slow down"),
			Code:    "429",
			Message: "Too many uploads. Try again in " + strconv.Itoa(seconds) + " seconds.",
		})
	}
}

// rateLimitKey identifies the caller for rate limiting purposes.
func (s *Server) rateLimitKey(r *http.Request) string {
	if user := currentUser(r.Context()); user != nil {
		return "user:" + strconv.FormatInt(user.ID, 10)
	}
	return "ip:" + s.clientIP(r)
}

// clientIP returns the address to bucket anonymous uploads under.
//
// Proxy headers are only consulted when explicitly trusted: they are otherwise
// trivially spoofable, which would let a caller sidestep the limit entirely.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxyHeaders {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			// The left-most entry is the original client.
			first, _, _ := strings.Cut(forwarded, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			return real
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// clearSessionCookie expires the session cookie.
func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.SecureCookies || isSecureRequest(r),
	})
}

// rateLimitLogins bounds sign-in attempts.
//
// This matters more once a second factor exists: a six-digit code is small
// enough that unlimited guesses would eventually find one, so the code prompt
// shares the budget with the password step.
func (s *Server) rateLimitLogins(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.logins.Enabled() {
			next(w, r)
			return
		}

		// Every authentication step shares the address budget. A supplied
		// username is not an identity, particularly on the second-factor form,
		// and changing it must not create a fresh budget.
		key := "login:" + s.clientIP(r)

		if ok, retryAfter := s.logins.Allow(key); !ok {
			seconds := int(retryAfter.Seconds()) + 1
			s.log.Warn("sign-in rate limited", "key", key, "retry_after_s", seconds, "remote", r.RemoteAddr)

			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			http.Error(w, "Too many sign-in attempts. Try again in "+strconv.Itoa(seconds)+" seconds.",
				http.StatusTooManyRequests)
			return
		}

		next(w, r)
	}
}
