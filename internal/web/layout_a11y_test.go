// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestLayoutHasASkipLinkAndMarksTheCurrentPage(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	member := h.sessionFor(t, alice.ID)

	for path, link := range map[string]string{
		"/recent":           `<a href="/recent" aria-current="page">`,
		"/gallery":          `<a href="/gallery" aria-current="page">`,
		"/albums":           `<a href="/albums" aria-current="page">`,
		"/settings/account": `<a href="/settings/account" aria-current="page">`,
	} {
		resp, page := member.get(path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d", path, resp.StatusCode)
		}
		skip := strings.Index(page, `<a class="skip-link" href="#main">Skip to content</a>`)
		if skip < 0 || skip > strings.Index(page, `<header class="topbar">`) {
			t.Errorf("%s: the skip link is missing or not first in the body", path)
		}
		if !strings.Contains(page, `<main id="main"`) {
			t.Errorf("%s: the skip link has no target", path)
		}
		if !strings.Contains(page, link) {
			t.Errorf("%s: the current page is not marked", path)
		}
		if n := strings.Count(page, `aria-current="page"`); n != 1 {
			t.Errorf("%s: %d links marked current, want 1", path, n)
		}
	}

	// A path that is merely similar is not current.
	_, page := member.get("/gallery?q=%2Frecent")
	if strings.Contains(page, `<a href="/recent" aria-current="page">`) {
		t.Error("a query string marked another page current")
	}
}

func TestThemePickerComesFromOnePartial(t *testing.T) {
	layout := readTemplate(t, "layout.html")
	if strings.Count(layout, "data-theme-select") != 0 || strings.Count(layout, `{{template "theme_picker"}}`) != 2 {
		t.Error("the layout still duplicates the theme picker markup")
	}
	h := newHarness(t)
	_, page := h.get("/login")
	if strings.Count(page, "data-theme-select") != 1 {
		t.Error("a signed-out page should render the theme picker once")
	}
	// Head tags each sit on their own line.
	head := layout[strings.Index(layout, "<head>"):strings.Index(layout, "</head>")]
	if regexp.MustCompile(`>\s*<(link|script|meta)`).MatchString(strings.ReplaceAll(head, "\n", "\x00")) {
		t.Error("head tags are run together on one line")
	}
}

func TestAccountShortcutRedirects(t *testing.T) {
	h := newHarness(t)
	member := h.sessionFor(t, h.seedUser("alice").ID)
	member.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ := member.get("/account")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/settings/account" {
		t.Fatalf("/account = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, page := member.get("/recent")
	if strings.Contains(page, `href="/account"`) {
		t.Error("the navigation still links to /account")
	}
}

func readTemplate(t *testing.T, name string) string {
	t.Helper()
	source, err := templatesFS.ReadFile("templates/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(source)
}
