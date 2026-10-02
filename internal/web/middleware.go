// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
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

// securityHeadersMW sets the response headers every page and file needs.
//
// nosniff stops a browser second-guessing the content types this server
// chose, which matters for user uploads above all. The framing headers keep
// every page out of other sites' frames, so a button here cannot be dressed up
// as something else and clicked by somebody who thinks they are elsewhere;
// both forms are sent because older browsers only know X-Frame-Options. Only
// frame-ancestors is set in the policy: a full Content-Security-Policy would
// have to allow the inline Alpine expressions, and is the operator's choice.
func securityHeadersMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Content-Security-Policy", "frame-ancestors 'none'")
		header.Set("Referrer-Policy", "same-origin")
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
		c, err := r.Cookie(s.cookieName(r, sessionCookie))
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

// csrfMW implements CSRF protection. Mutating requests must echo the token via
// header or form field.
//
// A signed-in request's token is derived from its session, so it cannot be
// planted: a cookie set by a neighbouring subdomain, or left over from before
// sign-in, is not the token this session expects. A signed-out request has no
// session to bind to and uses a double-submit cookie, which is still what keeps
// a stranger from submitting the sign-in form on somebody's behalf.
func (s *Server) csrfMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.expectedCSRF(w, r)

		if isMutating(r.Method) && !isAPIKeyAuth(r.Context()) && !isAPIPath(r) {
			provided, err := providedCSRF(r)
			if err != nil {
				status := http.StatusBadRequest
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					status = http.StatusRequestEntityTooLarge
				}
				http.Error(w, "could not read form", status)
				return
			}
			if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				s.log.Warn("csrf rejection", "method", r.Method, "path", r.URL.Path)
				http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
				return
			}
			if !parseCheckedMultipart(w, r) {
				return
			}
		}

		next.ServeHTTP(w, r.WithContext(withCSRF(r.Context(), token)))
	})
}

// providedCSRF finds the token a mutating request carries.
//
// The header is what htmx sends, and costs nothing to read. A form posted
// without JavaScript carries the token as a field instead. An ordinary form is
// small and bounded, so it is parsed. A multipart body may be an upload of
// hundreds of megabytes, so only its first part is read, which is where the
// templates put the token: the rest of the body is left for the handler, and
// is never read for a request that turns out to have no token.
func providedCSRF(r *http.Request) (string, error) {
	if provided := r.Header.Get(csrfHeader); provided != "" {
		return provided, nil
	}
	if isFormEncoded(r) {
		// Do not use FormValue alone: it hides body-limit errors.
		if err := r.ParseForm(); err != nil {
			return "", err
		}
		return r.PostFormValue(csrfField), nil
	}
	return firstPartToken(r)
}

// parseCheckedMultipart parses a multipart body once its request has passed
// the CSRF check, so a handler reading r.Form sees its fields whether the form
// arrived as multipart or not. The upload endpoints are left alone: they parse
// their own body, after taking a processing slot. It writes the refusal itself
// and reports whether to continue.
func parseCheckedMultipart(w http.ResponseWriter, r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || isUploadPath(r) {
		return true
	}
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "could not read form", status)
		return false
	}
	return true
}

// csrfPeekLimit bounds how much of a multipart body is read to find the token.
const csrfPeekLimit = 16 << 10

// firstPartToken reads the CSRF field from the first part of a multipart body
// and puts back everything it consumed.
func firstPartToken(r *http.Request) (string, error) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return "", nil
	}

	original := r.Body
	var consumed bytes.Buffer
	reader := multipart.NewReader(io.TeeReader(io.LimitReader(original, csrfPeekLimit), &consumed), params["boundary"])
	defer func() {
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(consumed.Bytes()), original), original}
	}()

	part, err := reader.NextPart()
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return "", err
		}
		return "", nil
	}
	if part.FormName() != csrfField {
		return "", nil
	}
	value, err := io.ReadAll(io.LimitReader(part, 256))
	if err != nil {
		return "", nil
	}
	return string(value), nil
}

// expectedCSRF returns the token this request must carry, issuing the cookie
// that holds it when the browser does not already have the right one.
func (s *Server) expectedCSRF(w http.ResponseWriter, r *http.Request) string {
	current := ""
	if c, err := r.Cookie(s.cookieName(r, csrfCookie)); err == nil {
		current = c.Value
	}

	token := current
	if session, err := r.Cookie(s.cookieName(r, sessionCookie)); err == nil && session.Value != "" && currentUser(r.Context()) != nil {
		token = sessionCSRFToken(session.Value)
	} else if len(token) < 32 {
		token = ids.Token(32)
	}
	if token != current {
		s.setCSRFCookie(w, r, token)
	}
	return token
}

// sessionCSRFToken derives a session's CSRF token from its secret. The session
// token never leaves its HttpOnly cookie, so nobody without it can compute
// this, and the derivation is one-way, so publishing this reveals nothing.
func sessionCSRFToken(sessionToken string) string {
	mac := hmac.New(sha256.New, []byte(sessionToken))
	mac.Write([]byte("imvault-csrf-v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

// setCSRFCookie hands the browser the token its forms and htmx headers echo.
func (s *Server) setCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(r, csrfCookie),
		Value:    token,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secureCookies(r),
		HttpOnly: false, // the page must be able to read it
		MaxAge:   30 * 24 * 60 * 60,
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
// Even then only the right-most X-Forwarded-For entry counts. That is the one
// the trusted proxy itself appended; anything to its left arrived from the
// client, so a proxy configured to append rather than replace would otherwise
// let every request choose its own bucket.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxyHeaders {
		if forwarded := r.Header.Values("X-Forwarded-For"); len(forwarded) > 0 {
			entries := strings.Split(forwarded[len(forwarded)-1], ",")
			if ip := strings.TrimSpace(entries[len(entries)-1]); ip != "" {
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
		Name:     s.cookieName(r, sessionCookie),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secureCookies(r),
	})
}

// rateLimitLogins bounds sign-in attempts, and every other route that checks a
// credential or acts for an anonymous caller in a way worth repeating: asking
// for reset mail, registering, redeeming a reset link, and the current-password
// checks behind the settings pages.
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
