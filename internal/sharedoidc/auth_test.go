// SPDX-License-Identifier: AGPL-3.0-or-later
package sharedoidc

import (
	"context"
	"database/sql"
	"errors"
	"imvault/internal/suite"
	_ "modernc.org/sqlite"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type authProvider struct{ subject string }

func (p *authProvider) Authorize(_ context.Context, state, nonce, verifier string) (string, error) {
	return "https://id.example.org/authorize?state=" + state, nil
}
func (p *authProvider) Exchange(_ context.Context, code, nonce, verifier string) (suite.Identity, error) {
	return suite.Identity{Issuer: "https://id.example.org", Subject: p.subject}, nil
}
func TestSharedSignInExplicitMembershipSessionBindingAndAtomicity(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "identity.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("CREATE TABLE users(id INTEGER PRIMARY KEY,disabled INTEGER NOT NULL DEFAULT 0);INSERT INTO users(id) VALUES(1),(2)"); err != nil {
		t.Fatal(err)
	}
	provider := &authProvider{subject: "person"}
	signedIn := int64(0)
	hooks := suite.AuthHooks{Current: func(r *http.Request) (int64, string) {
		uid := int64(0)
		if c, err := r.Cookie("local_user"); err == nil {
			uid, _ = strconv.ParseInt(c.Value, 10, 64)
		}
		binding := ""
		if c, err := r.Cookie("local_session"); err == nil {
			binding = c.Value
		}
		return uid, binding
	}, CSRF: func(r *http.Request) (string, string) { return "csrf", "csrf-secret" }, SignIn: func(w http.ResponseWriter, r *http.Request, id int64) (string, error) {
		var disabled bool
		if err := db.QueryRow("SELECT disabled FROM users WHERE id=?", id).Scan(&disabled); err != nil || disabled {
			return "", errors.New("unavailable")
		}
		signedIn = id
		return "/", nil
	}}
	auth, err := suite.NewAuth(suite.AuthConfig{App: "test", Issuer: "https://id.example.org", ClientID: "client", BaseURL: "https://app.example.org"}, provider, db, hooks)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	auth.Register(mux)
	request := func(method, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://app.example.org"+path, strings.NewReader(form.Encode()))
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	local := []*http.Cookie{{Name: "local_user", Value: "1"}, {Name: "local_session", Value: "existing-session"}}
	if w := request("POST", "/auth/shared/link", nil, local...); w.Code != 403 {
		t.Fatal("link did not require CSRF", w.Code)
	}
	begin := func(link bool, user []*http.Cookie) (string, *http.Cookie) {
		t.Helper()
		path, method := "/auth/shared/start", "GET"
		if link {
			path, method = "/auth/shared/link", "POST"
		}
		w := request(method, path, url.Values{"csrf": {"csrf-secret"}}, user...)
		if w.Code != 303 {
			t.Fatal("start", w.Code, w.Body.String())
		}
		u, _ := url.Parse(w.Header().Get("Location"))
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatal("missing state cookie")
		}
		return u.Query().Get("state"), cookies[0]
	}
	callback := func(state string, cookie *http.Cookie, user []*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		cookies := append(append([]*http.Cookie{}, user...), cookie)
		return request("GET", "/auth/shared/callback?code=code&state="+state, nil, cookies...)
	}
	state, cookie := begin(true, local)
	changed := []*http.Cookie{{Name: "local_user", Value: "1"}, {Name: "local_session", Value: "different-session"}}
	if w := callback(state, cookie, changed); w.Code != 403 {
		t.Fatal("link accepted a changed local session", w.Code)
	}
	state, cookie = begin(true, local)
	if w := callback(state, cookie, local); w.Code != 303 {
		t.Fatal("link", w.Code, w.Body.String())
	}
	if w := callback(state, cookie, local); w.Code != 400 {
		t.Fatal("replayed callback", w.Code)
	}
	state, cookie = begin(false, nil)
	if w := callback(state, cookie, nil); w.Code != 303 || signedIn != 1 {
		t.Fatal("linked login", w.Code, signedIn)
	}
	other := []*http.Cookie{{Name: "local_user", Value: "2"}, {Name: "local_session", Value: "other-session"}}
	state, cookie = begin(true, other)
	if w := callback(state, cookie, other); w.Code != 409 {
		t.Fatal("identity was reassigned", w.Code)
	}
	var owner int64
	if err = db.QueryRow("SELECT user_id FROM comfy_identities").Scan(&owner); err != nil || owner != 1 {
		t.Fatal("identity owner changed", owner, err)
	}
	if _, err = db.Exec("UPDATE users SET disabled=1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	state, cookie = begin(false, nil)
	if w := callback(state, cookie, nil); w.Code != 403 {
		t.Fatal("disabled local account signed in", w.Code)
	}
	provider.subject = "unknown"
	state, cookie = begin(false, nil)
	if w := callback(state, cookie, nil); w.Code != 403 {
		t.Fatal("unknown identity admitted", w.Code)
	}
	var users int
	db.QueryRow("SELECT count(*) FROM users").Scan(&users)
	if users != 2 {
		t.Fatal("SSO automatically provisioned users")
	}
	if _, err = db.Exec("CREATE TRIGGER deny_link BEFORE INSERT ON comfy_identities BEGIN SELECT RAISE(ABORT,'fixture');END"); err != nil {
		t.Fatal(err)
	}
	state, cookie = begin(true, other)
	if w := callback(state, cookie, other); w.Code != 409 {
		t.Fatal("failed binding accepted")
	}
	var count int
	db.QueryRow("SELECT count(*) FROM comfy_identities").Scan(&count)
	if count != 1 {
		t.Fatal("failed link changed bindings")
	}
	if w := request("POST", "/auth/shared/unlink", url.Values{"csrf": {"csrf-secret"}}, other...); w.Code != 303 {
		t.Fatal("unlink", w.Code)
	}
	db.QueryRow("SELECT count(*) FROM comfy_identities").Scan(&count)
	if count != 1 {
		t.Fatal("another user disconnected the identity")
	}
	if w := request("POST", "/auth/shared/unlink", url.Values{"csrf": {"csrf-secret"}}, local...); w.Code != 303 {
		t.Fatal("unlink", w.Code)
	}
	db.QueryRow("SELECT count(*) FROM comfy_identities").Scan(&count)
	if count != 0 {
		t.Fatal("identity not disconnected")
	}
}
