// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"imvault/internal/models"
)

// Context keys. The unexported struct type keeps them collision-free.
type ctxKey struct{ name string }

var (
	userKey    = ctxKey{"user"}
	csrfKey    = ctxKey{"csrf"}
	apiAuthKey = ctxKey{"apiAuth"}
)

const (
	sessionCookie = "imvault_session"
	csrfCookie    = "imvault_csrf"
	csrfHeader    = "X-CSRF-Token"
	csrfField     = "csrf_token"

	// defaultPageSize is how many files a listing shows per page.
	defaultPageSize = 48
)

// hashToken is the one-way transform applied to session tokens before storage.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// currentUser returns the authenticated user, or nil for anonymous requests.
func currentUser(ctx context.Context) *models.User {
	u, _ := ctx.Value(userKey).(*models.User)
	return u
}

// withUser attaches an authenticated user to the request context.
func withUser(ctx context.Context, u *models.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// withCSRF attaches the request's CSRF token to the context.
func withCSRF(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfKey, token)
}

// withAPIKeyAuth marks the request as authenticated by a bearer token, which
// exempts it from CSRF validation.
func withAPIKeyAuth(ctx context.Context) context.Context {
	return context.WithValue(ctx, apiAuthKey, true)
}

// isAPIKeyAuth reports whether the request was authenticated with an API key.
func isAPIKeyAuth(ctx context.Context) bool {
	ok, _ := ctx.Value(apiAuthKey).(bool)
	return ok
}

// trimSpace is a short alias so form handling reads cleanly.
func trimSpace(s string) string { return strings.TrimSpace(s) }

// urlQueryEscape escapes a value for use in a query string.
func urlQueryEscape(s string) string { return url.QueryEscape(s) }

// grid assembles the card grid fragment for a listing.
//
// removePattern is a printf-style path (with %s for the file id) used by each
// tile's action button; pass "" for the default file-delete endpoint.
func (s *Server) grid(r *http.Request, files []*models.File, removable, selectable bool, removePattern, empty string) fileCardsView {
	return fileCardsView{
		base:          s.base(r, ""),
		Files:         files,
		Removable:     removable,
		Selectable:    selectable,
		RemovePattern: removePattern,
		Empty:         empty,
	}
}

// feedGrid is grid with attribution, for the pages whose point is what other
// people have shared rather than one account's own uploads.
func (s *Server) feedGrid(r *http.Request, files []*models.File, empty string) fileCardsView {
	view := s.grid(r, files, false, false, "", empty)
	view.ShowOwner = true
	return view
}

func csrfToken(ctx context.Context) string {
	t, _ := ctx.Value(csrfKey).(string)
	return t
}

// isHTMX reports whether the request came from HTMX.
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// isSecureRequest reports whether the request reached us over TLS, directly or
// via a trusted reverse proxy.
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// base builds the common template data for a request.
func (s *Server) base(r *http.Request, title string) base {
	// The navigation shows whether signup and anonymous uploads are open, so
	// the instance policy is read on nearly every render.
	policy := s.policy()

	user := currentUser(r.Context())

	b := base{
		Title:             title,
		User:              user,
		CSRFToken:         csrfToken(r.Context()),
		AnonUploads:       policy.AllowAnonymousUploads,
		SignupOpen:        policy.AllowSignup,
		SourceURL:         s.cfg.SourceURL,
		VisibilityLevels:  models.VisibilityLevels(),
		DefaultVisibility: policy.DefaultVisibility,
		Notice:            strings.TrimSpace(r.URL.Query().Get("notice")),
		Error:             strings.TrimSpace(r.URL.Query().Get("error")),
		CurrentPath:       r.URL.Path,
		OIDCName:          s.oidc.Name(),
	}

	// The report badge is only counted for the people who can act on it, so an
	// ordinary page render does not pay for a query nobody will look at.
	if user.CanModerate() {
		if open, err := s.store.CountOpenReports(r.Context()); err != nil {
			s.log.Warn("count open reports", "error", err)
		} else {
			b.OpenReports = open
		}
	}

	return b
}

// baseErr is base with an error message attached.
func (s *Server) baseErr(r *http.Request, title, message string) base {
	b := s.base(r, title)
	b.Error = message
	return b
}

// pagination derives page state from the request and a total row count.
func pagination(r *http.Request, total int) (int, paginationView) {
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	totalPages := (total + defaultPageSize - 1) / defaultPageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	q := r.URL.Query()
	q.Del("page")
	baseQuery := ""
	if encoded := q.Encode(); encoded != "" {
		baseQuery = encoded + "&"
	}

	return page, paginationView{
		Page:       page,
		TotalPages: totalPages,
		Total:      total,
		BaseQuery:  baseQuery,
	}
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

// absoluteURL builds an absolute URL for sharing, honouring a configured base
// URL and otherwise deriving one from the request.
func (s *Server) absoluteURL(r *http.Request, path string) string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL + path
	}
	scheme := "http"
	if isSecureRequest(r) {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host + path
}

// noStore marks a response as uncacheable.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
}

// immutableCache lets browsers cache content-addressed static assets hard.
func immutableCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=604800")
		next.ServeHTTP(w, r)
	})
}

// fsSub returns a subtree of an embedded filesystem.
func fsSub(fsys fs.FS, dir string) (fs.FS, error) {
	return fs.Sub(fsys, dir)
}

// redirectNotice sends the browser to path with a human-readable message.
func redirectNotice(w http.ResponseWriter, r *http.Request, path, key, message string) {
	q := url.Values{}
	if message != "" {
		q.Set(key, message)
	}
	target := path
	if encoded := q.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// hxRedirect tells HTMX to perform a client-side navigation.
func hxRedirect(w http.ResponseWriter, path string) {
	w.Header().Set("HX-Redirect", path)
	w.WriteHeader(http.StatusNoContent)
}
