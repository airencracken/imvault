// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
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
	// sessionCookie and csrfCookie are the names over plain HTTP. Secure
	// responses use them with hostCookiePrefix; see cookieName.
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

// secureRequest reports whether the request reached us over TLS, directly or
// via a reverse proxy this instance has been told to trust.
//
// X-Forwarded-Proto is only evidence when a trusted proxy set it, and only its
// right-most value, the one the nearest proxy wrote. Otherwise any client can
// send it, and the scheme of a link this server builds would be whatever the
// request claimed.
func (s *Server) secureRequest(r *http.Request) bool {
	return s.clients().ForwardedHTTPS(r)
}

// secureCookies reports whether cookies set on this response should carry the
// Secure flag.
func (s *Server) secureCookies(r *http.Request) bool {
	return s.cfg.SecureCookies || s.secureRequest(r)
}

// hostCookiePrefix makes a browser accept a cookie only when it is Secure, has
// Path=/ and names no Domain. A sibling subdomain, or a page on the same host
// reached over plain HTTP, can then neither set nor overwrite it, which is
// what planting a session or CSRF token would take.
const hostCookiePrefix = "__Host-"

// cookieName is the name the session and CSRF cookies have on this request.
// Plain HTTP cannot carry a __Host- cookie, so local development keeps the
// bare name. Only the name for the request's own mode is ever read: a bare
// cookie presented over HTTPS may have been set by anyone who could reach the
// host over HTTP, and is ignored.
func (s *Server) cookieName(r *http.Request, name string) string {
	if s.secureCookies(r) {
		return hostCookiePrefix + name
	}
	return name
}

// base builds the common template data for a request.
func (s *Server) base(r *http.Request, title string) base {
	// The navigation shows whether signup and anonymous uploads are open, so
	// the instance policy is read on nearly every render.
	policy := s.policy()
	notice, problem := s.flash(r)

	user := currentUser(r.Context())
	branding := s.branding()
	mascotURL := "/static/img/mascot.png"
	faviconURL := ""
	if current := s.settings.Load(); current != nil {
		if current.CustomMascot {
			mascotURL = "/branding/mascot"
		}
		if current.CustomFavicon {
			faviconURL = "/branding/favicon"
		}
	}

	b := base{
		Version:           s.cfg.Version,
		ShowVersion:       branding.ShowVersion,
		Title:             title,
		SiteName:          branding.SiteName,
		WelcomeTitle:      branding.WelcomeTitle,
		WelcomeText:       branding.WelcomeText,
		MascotURL:         mascotURL,
		FaviconURL:        faviconURL,
		User:              user,
		CSRFToken:         csrfToken(r.Context()),
		AnonUploads:       policy.AllowAnonymousUploads,
		SignupOpen:        policy.AllowSignup && !policy.InviteOnly,
		SourceURL:         branding.SourceURL,
		VisibilityLevels:  models.VisibilityLevels(),
		DefaultVisibility: policy.DefaultVisibility,
		Notice:            notice,
		Error:             problem,
		CurrentPath:       r.URL.Path,
		MetadataLevels:    models.MetadataLevels(),
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
	if s.secureRequest(r) {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host + path
}

// errNoBaseURL is reported when a link has to leave the browser and there is
// no configured address to anchor it to.
var errNoBaseURL = errors.New("IMVAULT_BASE_URL is required to send links by email")

// emailLink builds an absolute URL for a message that leaves this server.
//
// Unlike absoluteURL it never falls back to the request. A link in an email is
// followed later, by somebody else, and a Host header is whatever the sender of
// the request wanted it to be: deriving a reset link from it would let anybody
// mail a real token to an address of their choosing.
func (s *Server) emailLink(path string) (string, error) {
	if s.cfg.BaseURL == "" {
		return "", errNoBaseURL
	}
	return s.cfg.BaseURL + path, nil
}

// closeLogged closes something whose failure to close cannot change the
// outcome any more, recording it rather than losing it.
func (s *Server) closeLogged(c io.Closer, what string) {
	if err := c.Close(); err != nil {
		s.log.Warn("close "+what, "error", err)
	}
}

// removeLogged removes a temporary file, recording a failure other than the
// file already being gone.
func (s *Server) removeLogged(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.log.Warn("remove temporary file", "path", path, "error", err)
	}
}

// writeBody writes a response body. Once the status is out, a failed write
// means the client went away; there is nobody left to tell.
func (s *Server) writeBody(w io.Writer, data []byte) {
	if _, err := w.Write(data); err != nil {
		s.log.Debug("write response", "error", err)
	}
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

// hxRedirect tells HTMX to perform a client-side navigation.
func hxRedirect(w http.ResponseWriter, path string) {
	w.Header().Set("HX-Redirect", path)
	w.WriteHeader(http.StatusNoContent)
}
