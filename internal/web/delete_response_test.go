// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The file page's Delete button is a plain form. It has to be answered with a
// redirect a browser follows, not an htmx instruction it ignores.
func TestPlainDeleteFormRedirects(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	_, body := h.upload("a.png")
	id := firstFileID(t, body)

	resp, _ := h.postForm("/f/"+id+"/delete", url.Values{"csrf_token": {h.csrf()}, "next": {"/gallery"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("plain delete = %d, want 303", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); !strings.HasPrefix(location, "/gallery") {
		t.Errorf("plain delete went to %q", location)
	}
	if resp.Header.Get("HX-Redirect") != "" {
		t.Error("a plain form was answered with an htmx redirect")
	}
}

// htmx still gets its own instruction, and an off-site next is never followed.
func TestHTMXDeleteKeepsItsRedirect(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	for _, tc := range []struct{ next, want string }{{"/gallery", "/gallery"}, {"//evil.example", ""}} {
		_, body := h.upload("a.png")
		id := firstFileID(t, body)
		req, err := http.NewRequest(http.MethodPost, h.server.URL+"/f/"+id+"/delete",
			strings.NewReader(url.Values{"next": {tc.next}}.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set(csrfHeader, h.csrf())
		req.Header.Set("HX-Request", "true")
		resp, err := h.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		closeBody(t, resp)
		if got := resp.Header.Get("HX-Redirect"); got != tc.want {
			t.Errorf("next=%q: HX-Redirect = %q, want %q", tc.next, got, tc.want)
		}
	}
}
