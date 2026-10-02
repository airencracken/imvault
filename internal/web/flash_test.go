// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/config"
)

const forgedFlash = "Your account is suspended. Email recovery@evil.example"

// A message in a link is only shown when this server put it there.
func TestUnsignedFlashMessagesAreNotShown(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	for _, path := range []string{"/", "/gallery", "/settings/account", "/settings/2fa", "/admin", "/admin/settings", "/admin/users", "/invites"} {
		for _, kind := range []string{"notice", "error"} {
			_, page := h.get(path + "?" + kind + "=" + url.QueryEscape(forgedFlash))
			if strings.Contains(page, "recovery@evil.example") {
				t.Errorf("%s showed an unsigned %s", path, kind)
			}
		}
	}
}

func TestTamperedFlashMessagesAreNotShown(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")

	signed, err := url.Parse(h.srv.flashURL("/gallery", flashNotice, "Album saved."))
	if err != nil {
		t.Fatal(err)
	}
	q := signed.Query()

	// The genuine message is shown.
	if _, page := h.get(signed.String()); !strings.Contains(page, "Album saved.") {
		t.Fatal("a signed message was not shown")
	}

	// A different message under the same signature is not.
	q.Set("notice", forgedFlash)
	if _, page := h.get("/gallery?" + q.Encode()); strings.Contains(page, "recovery@evil.example") {
		t.Error("an altered message was shown under the original signature")
	}

	// Nor is the same text moved to the other kind.
	q = signed.Query()
	q.Set("error", q.Get("notice"))
	q.Del("notice")
	if _, page := h.get("/gallery?" + q.Encode()); strings.Contains(page, "Album saved.") {
		t.Error("a notice signature was accepted for an error")
	}
}

// Redirects the server makes still carry their message to the next page.
func TestRedirectsCarryTheirMessage(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	resp, _ := h.postForm("/albums", url.Values{"title": {""}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("empty album = %d", resp.StatusCode)
	}
	_, page := h.get(resp.Header.Get("Location"))
	if !strings.Contains(page, "An album needs a title.") {
		t.Error("the redirect lost its message")
	}
}

func cookieSecure(resp *http.Response, name string) (bool, bool) {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c.Secure, true
		}
	}
	return false, false
}

// X-Forwarded-Proto is a claim any client can make, so it only counts when the
// operator has said a proxy sets it.
func TestForwardedProtoNeedsATrustedProxy(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		h := newHarnessWith(t, func(c *config.Config) { c.TrustProxyHeaders = trusted })
		req, err := http.NewRequest(http.MethodGet, h.server.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Forwarded-Proto", "https")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		closeBody(t, resp)
		secure, found := cookieSecure(resp, csrfCookie)
		if !found {
			t.Fatal("no CSRF cookie was set")
		}
		if secure != trusted {
			t.Errorf("trusted=%v: Secure = %v", trusted, secure)
		}
		if got := h.srv.absoluteURL(req, "/x"); strings.HasPrefix(got, "https://") != trusted {
			t.Errorf("trusted=%v: absolute URL %q", trusted, got)
		}
	}
}
