// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSafeNextAcceptsLocalPaths(t *testing.T) {
	for _, target := range []string{
		"/", "/gallery", "/a/raid-night?page=2", "/tags/bob/holiday#top",
		"/f/abc123/raw", "/search?q=a%2Fb", "/upload",
	} {
		if got := safeNext(target); got != target {
			t.Errorf("safeNext(%q) = %q, want it kept", target, got)
		}
	}
}

// The adversarial inputs: everything here would leave the site in at least one
// browser, or is not a path at all.
func TestSafeNextRefusesEverythingElse(t *testing.T) {
	for _, target := range []string{
		"", "gallery", "//evil.example", "///evil.example",
		`/\evil.example`, `/\/evil.example`, `\\evil.example`,
		"/\t/evil.example", "/\n/evil.example", "/\r/evil.example", "/ /evil.example",
		"/\x00/evil.example", "/\x7f", "https://evil.example", "http:/evil.example",
		"javascript:alert(1)", "/∕evil.example", "/%zz", "/" + strings.Repeat("a", maxNextLength),
		"\t//evil.example", " /gallery",
	} {
		if got := safeNext(target); got != "" {
			t.Errorf("safeNext(%q) = %q, want it refused", target, got)
		}
	}
}

// The sign-in form carries next through a hidden field, which is how an open
// redirect would actually be delivered.
func TestLoginDoesNotRedirectOffSite(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	for _, next := range []string{`/\evil.example`, "/\t/evil.example", "//evil.example"} {
		s := h.newSession(t)
		_, page := s.get("/login?next=" + url.QueryEscape(next))
		if strings.Contains(page, "evil.example") {
			t.Errorf("the login page carried %q into its form", next)
		}
		resp, _ := s.post("/login", url.Values{"username": {"boss"}, "password": {testPassword}, "next": {next}})
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("login = %d", resp.StatusCode)
		}
		if location := resp.Header.Get("Location"); location != "/gallery" {
			t.Errorf("next=%q redirected to %q, want /gallery", next, location)
		}
	}
}

// browserTarget approximates what a browser does to a Location value before
// resolving it: strip tabs and newlines, and read backslashes as slashes.
func browserTarget(location string) string {
	location = strings.NewReplacer("\t", "", "\n", "", "\r", "", `\`, "/").Replace(location)
	return strings.TrimLeft(location, " \x00")
}

func FuzzSafeNext(f *testing.F) {
	for _, seed := range []string{
		"/gallery", "//evil.example", `/\evil.example`, "/\t/evil.example",
		"https://evil.example", "/a/../..//evil.example", "/.//evil.example", "/%2F%2Fevil",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got := safeNext(input)
		if got == "" {
			return
		}
		if got != input {
			t.Fatalf("safeNext rewrote %q to %q", input, got)
		}
		parsed, err := url.Parse(got)
		if err != nil || parsed.Scheme != "" || parsed.Host != "" {
			t.Fatalf("accepted %q, which parses with a scheme or host", got)
		}

		// Follow it through the real redirect, then through what a browser
		// would make of the header.
		rec := httptest.NewRecorder()
		http.Redirect(rec, httptest.NewRequest(http.MethodPost, "/login", nil), got, http.StatusSeeOther)
		location := browserTarget(rec.Header().Get("Location"))
		if !strings.HasPrefix(location, "/") || strings.HasPrefix(location, "//") {
			t.Fatalf("accepted %q, which a browser would follow to %q", got, location)
		}
		resolved, err := url.Parse("https://photos.example.net/login")
		if err != nil {
			t.Fatal(err)
		}
		target, err := resolved.Parse(location)
		if err == nil && target.Host != "photos.example.net" {
			t.Fatalf("accepted %q, which resolves to host %q", got, target.Host)
		}
	})
}
