// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"imvault/internal/config"
	"imvault/internal/ids"
	"imvault/internal/store"
)

// cookieMode is one way a request can reach the server, and whether its
// cookies are meant to be Secure.
type cookieMode struct {
	name    string
	secure  bool
	config  func(*config.Config)
	headers http.Header
}

var cookieModes = []cookieMode{
	{name: "plain HTTP", secure: false},
	{name: "configured secure", secure: true, config: func(c *config.Config) { c.SecureCookies = true }},
	{
		name: "trusted proxy", secure: true,
		config:  func(c *config.Config) { trustLoopback(c, true) },
		headers: http.Header{"X-Forwarded-Proto": {"https"}},
	},
	// A client claiming HTTPS through an untrusted header is still plain HTTP.
	{name: "untrusted forwarded scheme", secure: false, headers: http.Header{"X-Forwarded-Proto": {"https"}}},
}

func (m cookieMode) want(name string) string {
	if m.secure {
		return hostCookiePrefix + name
	}
	return name
}

func (m cookieMode) other(name string) string {
	if m.secure {
		return name
	}
	return hostCookiePrefix + name
}

// rawRequest sends a request with exactly the given cookies. A cookie jar
// would drop Secure cookies over the test server's plain HTTP.
func (h *harness) rawRequest(m cookieMode, method, path string, form url.Values, cookies ...*http.Cookie) *http.Response {
	h.t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req, err := http.NewRequest(method, h.server.URL+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	for name, values := range m.headers {
		req.Header[name] = values
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	closeBody(h.t, resp)
	return resp
}

func responseCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// checkCookie requires the attributes a __Host- cookie must have, and that
// the other mode's name was never set.
func checkCookie(t *testing.T, m cookieMode, resp *http.Response, name string) *http.Cookie {
	t.Helper()
	c := responseCookie(resp, m.want(name))
	if c == nil {
		t.Fatalf("%s: no %s cookie in %v", m.name, m.want(name), resp.Header.Values("Set-Cookie"))
	}
	if c.Secure != m.secure || c.Path != "/" || c.Domain != "" {
		t.Fatalf("%s: %s has Secure=%v Path=%q Domain=%q", m.name, c.Name, c.Secure, c.Path, c.Domain)
	}
	if responseCookie(resp, m.other(name)) != nil {
		t.Fatalf("%s: also set the other mode's %s", m.name, m.other(name))
	}
	return c
}

// The session and CSRF cookies take the __Host- prefix exactly when they are
// Secure, through sign-in, signed-in requests and sign-out.
func TestSessionAndCSRFCookieNamesFollowTheMode(t *testing.T) {
	for _, m := range cookieModes {
		t.Run(m.name, func(t *testing.T) {
			h := newHarnessWith(t, m.config)
			if _, err := h.store.CreateUser(t.Context(), store.NewUser{
				Username: "alice", Email: "alice@example.com", PasswordHash: mustHashPassword(t, "a-long-enough-password"),
			}); err != nil {
				t.Fatal(err)
			}

			csrf := checkCookie(t, m, h.rawRequest(m, http.MethodGet, "/login", nil), csrfCookie)
			if csrf.HttpOnly {
				t.Fatal("the page cannot read an HttpOnly CSRF cookie")
			}

			resp := h.rawRequest(m, http.MethodPost, "/login", url.Values{
				"username": {"alice"}, "password": {"a-long-enough-password"}, csrfField: {csrf.Value},
			}, csrf)
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("sign-in = %d", resp.StatusCode)
			}
			session := checkCookie(t, m, resp, sessionCookie)
			if !session.HttpOnly || session.Value == "" {
				t.Fatal("session cookie is readable by scripts or empty")
			}
			bound := checkCookie(t, m, resp, csrfCookie)
			if bound.Value != sessionCSRFToken(session.Value) {
				t.Fatal("sign-in did not bind the CSRF token to the session")
			}

			if resp := h.rawRequest(m, http.MethodGet, "/settings/account", nil, session, bound); resp.StatusCode != http.StatusOK {
				t.Fatalf("signed-in page = %d", resp.StatusCode)
			}

			resp = h.rawRequest(m, http.MethodPost, "/logout", url.Values{csrfField: {bound.Value}}, session, bound)
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("sign-out = %d", resp.StatusCode)
			}
			if cleared := checkCookie(t, m, resp, sessionCookie); cleared.MaxAge >= 0 {
				t.Fatal("sign-out did not expire the session cookie")
			}
		})
	}
}

// A cookie under the other mode's name is not read at all. Over HTTPS a bare
// cookie may have been planted by anyone who could reach the host over HTTP,
// and over HTTP no browser would have sent a __Host- one.
func TestWrongModeCookiesAreIgnored(t *testing.T) {
	for _, m := range cookieModes {
		t.Run(m.name, func(t *testing.T) {
			h := newHarnessWith(t, m.config)
			user := h.seedUser("alice")
			token := ids.Token(32)
			if err := h.store.CreateSession(t.Context(), hashToken(token), user.ID, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}

			wrong := &http.Cookie{Name: m.other(sessionCookie), Value: token}
			if resp := h.rawRequest(m, http.MethodGet, "/settings/account", nil, wrong); resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("the other mode's session cookie signed in: %d", resp.StatusCode)
			}
			right := &http.Cookie{Name: m.want(sessionCookie), Value: token}
			if resp := h.rawRequest(m, http.MethodGet, "/settings/account", nil, right); resp.StatusCode != http.StatusOK {
				t.Fatalf("the session cookie did not sign in: %d", resp.StatusCode)
			}

			planted := strings.Repeat("p", 64)
			genuine := strings.Repeat("g", 64)
			form := func(token string) url.Values {
				return url.Values{"username": {"nobody"}, "password": {"wrong-password-here"}, csrfField: {token}}
			}
			// Double-submit with only the other mode's cookie.
			if resp := h.rawRequest(m, http.MethodPost, "/login", form(planted),
				&http.Cookie{Name: m.other(csrfCookie), Value: planted}); resp.StatusCode != http.StatusForbidden {
				t.Fatalf("the other mode's CSRF cookie was accepted: %d", resp.StatusCode)
			}
			// With both present, only this mode's value counts.
			both := []*http.Cookie{
				{Name: m.other(csrfCookie), Value: planted},
				{Name: m.want(csrfCookie), Value: genuine},
			}
			if resp := h.rawRequest(m, http.MethodPost, "/login", form(planted), both...); resp.StatusCode != http.StatusForbidden {
				t.Fatalf("the other mode's CSRF value was used: %d", resp.StatusCode)
			}
			if resp := h.rawRequest(m, http.MethodPost, "/login", form(genuine), both...); resp.StatusCode == http.StatusForbidden {
				t.Fatal("this mode's CSRF cookie was refused")
			}

			// A signed-in request whose session arrives under the wrong name
			// is anonymous, so its derived token does not apply either.
			if resp := h.rawRequest(m, http.MethodPost, "/logout", url.Values{csrfField: {sessionCSRFToken(token)}},
				wrong, &http.Cookie{Name: m.other(csrfCookie), Value: sessionCSRFToken(token)}); resp.StatusCode != http.StatusForbidden {
				t.Fatalf("a wrong-mode session and token passed the CSRF check: %d", resp.StatusCode)
			}
		})
	}
}

// Whatever a client sends, a Secure response never sets a bare session or
// CSRF cookie, and a __Host- cookie always keeps the attributes the prefix
// demands.
func TestHostPrefixedCookiesKeepTheirAttributes(t *testing.T) {
	for _, m := range cookieModes {
		h := newHarnessWith(t, m.config)
		for _, path := range []string{"/", "/login", "/register", "/gallery", "/settings/account", "/no-such-page"} {
			for _, sent := range [][]*http.Cookie{
				nil,
				{{Name: sessionCookie, Value: "forged"}},
				{{Name: hostCookiePrefix + sessionCookie, Value: "forged"}},
				{{Name: csrfCookie, Value: "short"}, {Name: hostCookiePrefix + csrfCookie, Value: "x"}},
			} {
				resp := h.rawRequest(m, http.MethodGet, path, nil, sent...)
				for _, c := range resp.Cookies() {
					if strings.HasPrefix(c.Name, hostCookiePrefix) && (!c.Secure || c.Path != "/" || c.Domain != "") {
						t.Errorf("%s %s: %s breaks the __Host- rules", m.name, path, c.Name)
					}
					if m.secure && (c.Name == sessionCookie || c.Name == csrfCookie) {
						t.Errorf("%s %s: set bare %s on a secure response", m.name, path, c.Name)
					}
					if !m.secure && strings.HasPrefix(c.Name, hostCookiePrefix) {
						t.Errorf("%s %s: set %s over plain HTTP", m.name, path, c.Name)
					}
				}
			}
		}
	}
}
