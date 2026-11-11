// SPDX-License-Identifier: AGPL-3.0-or-later
package suite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Identity struct{ Issuer, Subject string }
type Provider interface {
	Authorize(context.Context, string, string, string) (string, error)
	Exchange(context.Context, string, string, string) (Identity, error)
}
type AuthConfig struct{ App, Issuer, ClientID, ClientSecret, BaseURL string }
type AuthHooks struct {
	// RateKey uses the application's trusted-proxy and client-network policy.
	RateKey func(*http.Request) string
	Current func(*http.Request) (int64, string)
	CSRF    func(*http.Request) (string, string)
	SignIn  func(http.ResponseWriter, *http.Request, int64) (string, error)
}
type authFlow struct {
	Nonce, Verifier, Remote string
	User                    int64
	Binding                 [32]byte
	Expires                 time.Time
}
type Auth struct {
	config   AuthConfig
	provider Provider
	db       *sql.DB
	hooks    AuthHooks
	mu       sync.Mutex
	flows    map[string]authFlow
}

func NewAuth(c AuthConfig, p Provider, db *sql.DB, h AuthHooks) (*Auth, error) {
	if !validAuthConfig(c, p, db, h) {
		return nil, errors.New("community sign-in needs an issuer, client ID and public application address")
	}
	if !validAuthIssuer(c.Issuer) {
		return nil, errors.New("community sign-in issuer must use HTTPS")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS comfy_identities(issuer TEXT NOT NULL,subject TEXT NOT NULL,user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,PRIMARY KEY(issuer,subject),UNIQUE(issuer,user_id))`)
	if err != nil {
		return nil, err
	}
	return &Auth{config: c, provider: p, db: db, hooks: h, flows: map[string]authFlow{}}, nil
}
func randomAuthToken() string {
	data := make([]byte, 32)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}
func (a *Auth) cookieName() string {
	prefix := ""
	if strings.HasPrefix(a.config.BaseURL, "https://") {
		prefix = "__Host-"
	}
	return prefix + a.config.App + "_shared_state"
}
func (a *Auth) flowCookie(w http.ResponseWriter, value string, age int) {
	// #nosec G124 -- Secure follows the configured public URL; HTTP loopback demos remain supported.
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: value, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(a.config.BaseURL, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: age})
}

// Register installs opt-in routes. Linking never creates a local account or
// assigns roles: an existing local session must explicitly connect an identity.
func (a *Auth) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/shared/start", func(w http.ResponseWriter, r *http.Request) { a.begin(w, r, false) })
	mux.HandleFunc("GET /auth/shared/callback", a.callback)
	mux.HandleFunc("GET /auth/shared/manage", a.manage)
	mux.HandleFunc("POST /auth/shared/link", func(w http.ResponseWriter, r *http.Request) {
		if a.validForm(w, r) {
			a.begin(w, r, true)
		}
	})
	mux.HandleFunc("POST /auth/shared/unlink", a.unlink)
}
func (a *Auth) validForm(w http.ResponseWriter, r *http.Request) bool {
	uid, _ := a.hooks.Current(r)
	if uid == 0 {
		http.Error(w, "Sign in locally first", http.StatusUnauthorized)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return false
	}
	key, want := a.hooks.CSRF(r)
	values := r.PostForm[key]
	if want == "" || len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(want)) != 1 {
		http.Error(w, "The form expired; try again", http.StatusForbidden)
		return false
	}
	return true
}
func (a *Auth) admit(state string, flow authFlow) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	same := 0
	for key, old := range a.flows {
		if time.Now().After(old.Expires) {
			delete(a.flows, key)
		} else if old.Remote == flow.Remote {
			same++
		}
	}
	if len(a.flows) >= 1024 || same >= 16 {
		return false
	}
	a.flows[state] = flow
	return true
}
func (a *Auth) begin(w http.ResponseWriter, r *http.Request, link bool) {
	uid, binding := a.hooks.Current(r)
	if !link && uid != 0 {
		http.Redirect(w, r, "/auth/shared/manage", http.StatusSeeOther)
		return
	}
	state := randomAuthToken()
	flow := authFlow{Nonce: randomAuthToken(), Verifier: randomAuthToken(), Remote: a.requestKey(r), Expires: time.Now().Add(5 * time.Minute)}
	if link {
		flow.User = uid
		flow.Binding = sha256.Sum256([]byte(binding))
	}
	if !a.admit(state, flow) {
		http.Error(w, "Too many pending sign-in requests; try again later", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	destination, err := a.provider.Authorize(ctx, state, flow.Nonce, flow.Verifier)
	if err != nil {
		a.mu.Lock()
		delete(a.flows, state)
		a.mu.Unlock()
		http.Error(w, "Community sign-in is temporarily unavailable", http.StatusBadGateway)
		return
	}
	a.flowCookie(w, state, 300)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, destination, http.StatusSeeOther)
}
func (a *Auth) takeFlow(w http.ResponseWriter, r *http.Request) (authFlow, bool) {
	state := r.URL.Query()["state"]
	cookie, err := r.Cookie(a.cookieName())
	if len(state) != 1 || err != nil || len(state[0]) != 64 || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state[0])) != 1 {
		return authFlow{}, false
	}
	a.mu.Lock()
	flow, ok := a.flows[state[0]]
	delete(a.flows, state[0])
	a.mu.Unlock()
	a.flowCookie(w, "", -1)
	return flow, ok && time.Now().Before(flow.Expires)
}
func (a *Auth) callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	flow, ok := a.takeFlow(w, r)
	codes := r.URL.Query()["code"]
	if !ok || len(codes) != 1 || codes[0] == "" || len(codes[0]) > 4096 {
		http.Error(w, "Sign-in expired; start again", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	identity, err := a.provider.Exchange(ctx, codes[0], flow.Nonce, flow.Verifier)
	if err != nil || identity.Issuer != a.config.Issuer || identity.Subject == "" || len(identity.Subject) > 1024 {
		http.Error(w, "Community sign-in could not be verified", http.StatusUnauthorized)
		return
	}
	if flow.User != 0 {
		a.completeLink(w, r, flow, identity)
		return
	}
	var uid int64
	if err = a.db.QueryRowContext(ctx, "SELECT user_id FROM comfy_identities WHERE issuer=? AND subject=?", identity.Issuer, identity.Subject).Scan(&uid); err != nil {
		http.Error(w, "Sign in locally, then connect your community account from Account", http.StatusForbidden)
		return
	}
	destination, err := a.hooks.SignIn(w, r, uid)
	if err != nil {
		http.Error(w, "This local account cannot sign in", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(destination, "/") || strings.HasPrefix(destination, "//") {
		destination = "/"
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
}
func (a *Auth) completeLink(w http.ResponseWriter, r *http.Request, flow authFlow, identity Identity) {
	uid, binding := a.hooks.Current(r)
	digest := sha256.Sum256([]byte(binding))
	if uid != flow.User || binding == "" || subtle.ConstantTimeCompare(digest[:], flow.Binding[:]) != 1 {
		http.Error(w, "Your local session changed; connect your account again", http.StatusForbidden)
		return
	}
	_, err := a.db.ExecContext(r.Context(), "INSERT INTO comfy_identities(issuer,subject,user_id) VALUES(?,?,?)", identity.Issuer, identity.Subject, uid)
	if err != nil {
		http.Error(w, "This community account is already connected; disconnect it before changing accounts", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/auth/shared/manage", http.StatusSeeOther)
}

var manageTemplate = template.Must(template.New("shared").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><title>Community sign-in</title><link rel="stylesheet" href="{{.CSS}}"><main class="comfy-auth"><h1>Community sign-in</h1><p>Use your community account to sign in to <strong>{{.Name}}</strong>. Membership and permissions stay with each app.</p>{{if .Linked}}<p>Your community account is connected.</p><form method="post" action="/auth/shared/unlink"><input type="hidden" name="{{.Key}}" value="{{.Token}}"><button>Disconnect community account</button></form>{{else}}<p>Connect an existing local account. Your local password continues to work.</p><form method="post" action="/auth/shared/link"><input type="hidden" name="{{.Key}}" value="{{.Token}}"><button>Connect community account</button></form>{{end}}<p><a href="/account">Return to your account</a></p></main></html>`))

func (a *Auth) manage(w http.ResponseWriter, r *http.Request) {
	uid, _ := a.hooks.Current(r)
	if uid == 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	var linked bool
	if err := a.db.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM comfy_identities WHERE user_id=? AND issuer=?)", uid, a.config.Issuer).Scan(&linked); err != nil {
		http.Error(w, "Account settings unavailable", http.StatusServiceUnavailable)
		return
	}
	key, csrf := a.hooks.CSRF(r)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = manageTemplate.Execute(w, struct {
		Linked                bool
		Key, Token, CSS, Name string
	}{linked, key, csrf, a.styleURL(), (App{ID: a.config.App}).Name()})
}
func (a *Auth) unlink(w http.ResponseWriter, r *http.Request) {
	if !a.validForm(w, r) {
		return
	}
	uid, _ := a.hooks.Current(r)
	if _, err := a.db.ExecContext(r.Context(), "DELETE FROM comfy_identities WHERE user_id=? AND issuer=?", uid, a.config.Issuer); err != nil {
		http.Error(w, "Could not disconnect account", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/auth/shared/manage", http.StatusSeeOther)
}

func remoteAuthKey(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		return host
	}
	return remote
}

func (a *Auth) styleURL() string {
	if a.config.App == "imvault" {
		return "/static/css/comfy-components.css"
	}
	return "/static/comfy-components.css"
}

func validAuthConfig(c AuthConfig, p Provider, db *sql.DB, h AuthHooks) bool {
	return c.App != "" && c.Issuer != "" && c.ClientID != "" && p != nil && db != nil && h.Current != nil && h.CSRF != nil && h.SignIn != nil && validAppURL(c.BaseURL)
}
func validAuthIssuer(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || !validAppURL(raw) {
		return false
	}
	return u.Scheme == "https" || u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
}

func (a *Auth) requestKey(r *http.Request) string {
	if a.hooks.RateKey != nil {
		if key := a.hooks.RateKey(r); key != "" {
			return key
		}
	}
	return remoteAuthKey(r.RemoteAddr)
}
