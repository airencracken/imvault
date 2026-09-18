// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// postHTMX sends a form as htmx would, so the handler answers with a fragment.
func (h *harness) postHTMX(path string, form url.Values) (*http.Response, string) {
	h.t.Helper()

	req, err := http.NewRequest(http.MethodPost, h.server.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		h.t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")

	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()

	body := readAll(h.t, resp.Body)
	return resp, body
}

func TestAdminActionsAnswerHTMXWithAFragment(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	target := h.seedUser("alice")

	resp, body := h.postHTMX("/admin/users/"+itoa64(target.ID)+"/quota", url.Values{
		"csrf_token": {h.csrf()},
		"quota_mb":   {"3"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("quota = %d, want 200 (body: %s)", resp.StatusCode, truncate(body))
	}

	// A fragment, not a whole document.
	if strings.Contains(body, "<html") || strings.Contains(body, "<!doctype") {
		t.Error("an htmx request was answered with a full page")
	}
	if !strings.Contains(body, `id="user-`+itoa64(target.ID)+`"`) {
		t.Errorf("the response is not the affected row: %s", truncate(body))
	}
	// The row carries the new state, so the client does not have to guess.
	if !strings.Contains(body, "3.0 MiB") {
		t.Errorf("the row does not reflect the new quota: %s", truncate(body))
	}
	// And the notice arrives out of band, to be placed elsewhere on the page.
	if !strings.Contains(body, `id="admin-notice"`) || !strings.Contains(body, "hx-swap-oob") {
		t.Errorf("no out-of-band notice in the response: %s", truncate(body))
	}
	if !strings.Contains(body, "Quota for alice set to") {
		t.Errorf("the notice does not say what happened: %s", truncate(body))
	}
}

func TestAdminRowsAreWiredForClientSideFiltering(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")
	h.seedUser("alice")

	_, page := h.get("/admin/users")

	// The filter counts rows by their data-search text, so every row must carry
	// it, and every row must bind the predicate that actually hides it. A row
	// with one but not the other makes the summary disagree with the screen.
	rows := strings.Split(page, "<tr ")
	found := 0
	for _, row := range rows {
		if !strings.Contains(row, "data-search=") {
			continue
		}
		found++
		if !strings.Contains(row, "x-show=\"matches($el)\"") {
			t.Errorf("a row carries data-search without the x-show binding:\n%.200s", row)
		}
	}
	if found < 2 {
		t.Fatalf("expected the table to have rows, found %d", found)
	}
}

func TestAdminDeleteRemovesTheRow(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	target := h.seedUser("alice")

	resp, body := h.postHTMX("/admin/users/"+itoa64(target.ID)+"/delete", url.Values{
		"csrf_token": {h.csrf()},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d, want 200", resp.StatusCode)
	}

	// An empty primary body with an outerHTML swap removes the row.
	if strings.Contains(body, "<tr") {
		t.Errorf("the deleted row was returned again: %s", truncate(body))
	}
	if !strings.Contains(body, "hx-swap-oob") {
		t.Errorf("no notice accompanied the removal: %s", truncate(body))
	}
	if !strings.Contains(body, "Deleted alice") {
		t.Errorf("the notice does not say what happened: %s", truncate(body))
	}

	// The account and its stored objects are gone.
	if _, err := h.store.UserByID(t.Context(), target.ID); err == nil {
		t.Error("the account survived")
	}
}

func TestAdminActionFailuresStayInline(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	target := h.seedUser("alice")

	// A quota that is not a number must not be silently applied, and must not
	// bounce the administrator to another page.
	resp, body := h.postHTMX("/admin/users/"+itoa64(target.ID)+"/quota", url.Values{
		"csrf_token": {h.csrf()},
		"quota_mb":   {"lots"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bad quota = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "whole number of MiB") {
		t.Errorf("the error is not shown inline: %s", truncate(body))
	}
	// The row is still returned, so the table is not left with a hole.
	if !strings.Contains(body, `id="user-`+itoa64(target.ID)+`"`) {
		t.Errorf("the row was not returned with the error: %s", truncate(body))
	}

	unchanged, err := h.store.UserByID(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.Unlimited() {
		t.Errorf("a malformed quota changed the value to %d", unchanged.QuotaBytes)
	}
}

func TestAdminLastAdministratorErrorIsInline(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}

	resp, body := h.postHTMX("/admin/users/"+itoa64(boss.ID)+"/admin", url.Values{
		"csrf_token": {h.csrf()},
		"admin":      {"0"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("self-demotion = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "cannot remove your own administrator rights") {
		t.Errorf("the refusal is not explained inline: %s", truncate(body))
	}

	after, err := h.store.UserByID(t.Context(), boss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.IsAdmin {
		t.Error("the last administrator was demoted")
	}
}

func TestAdminActionsStillWorkWithoutJavaScript(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	target := h.seedUser("alice")

	// The same requests without the htmx header get a redirect, so the plain
	// form fallback keeps working.
	resp, _ := h.postForm("/admin/users/"+itoa64(target.ID)+"/quota", url.Values{
		"csrf_token": {h.csrf()},
		"quota_mb":   {"2"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("non-htmx quota = %d, want 303", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); !strings.HasPrefix(location, "/admin/users") {
		t.Errorf("redirected to %q, want the account list", location)
	}

	updated, err := h.store.UserByID(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.QuotaBytes != 2*1024*1024 {
		t.Errorf("the fallback did not apply the quota (got %d)", updated.QuotaBytes)
	}
}

func TestAdminResetLinkSurvivesWithoutHTMX(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	target := h.seedUser("alice")

	// The link exists only in this response, so the fallback must render a page
	// rather than redirect: a redirect would lose it entirely.
	resp, page := h.postForm("/admin/users/"+itoa64(target.ID)+"/reset", url.Values{
		"csrf_token": {h.csrf()},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset without htmx = %d, want the rendered page", resp.StatusCode)
	}

	token := extractResetLink(t, page)
	if token == "" {
		t.Fatalf("the page does not carry the link: %s", truncate(page))
	}
	if resp, _ := h.get("/reset/" + token); resp.StatusCode != http.StatusOK {
		t.Errorf("the issued link does not work: %d", resp.StatusCode)
	}
}

func TestAdminMailRowsAreFragments(t *testing.T) {
	h := newQueueHarness(t)
	h.registerForm("boss")

	// Point the harness sender at a failure so a message stays queued.
	h.mailer.failWith = errFakeMail

	target := h.seedUser("alice")
	if err := h.store.SetEmail(t.Context(), target.ID, "alice@example.com", false); err != nil {
		t.Fatal(err)
	}
	h.get("/")
	h.postForm("/forgot", url.Values{"csrf_token": {h.csrf()}, "identifier": {"alice"}})

	messages, err := h.store.ListMail(t.Context(), 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("expected a queued message, got %d (%v)", len(messages), err)
	}

	// A retry while the relay is still down returns the row, still queued, with
	// the reason attached rather than a redirect.
	resp, body := h.postHTMX("/admin/mail/"+itoa64(messages[0].ID)+"/retry", url.Values{
		"csrf_token": {h.csrf()},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, `id="mail-`+itoa64(messages[0].ID)+`"`) {
		t.Errorf("the message row was not returned: %s", truncate(body))
	}
	if !strings.Contains(body, "relay unreachable") {
		t.Errorf("the failure reason is not shown in the row: %s", truncate(body))
	}
}

func TestPagesUsingAlpineAlsoLoadIt(t *testing.T) {
	h := newHarness(t)

	// The first account is the administrator, so the admin pages are reachable.
	h.registerForm("boss")

	// A page with Alpine attributes but no Alpine loaded is silently inert:
	// the confirmation dialog never opens, the filter never hides anything, and
	// nothing reports an error. This is the check that catches it.
	// "/" is not here: it redirects to the gallery once signed in, which is the
	// state this test needs in order to reach the admin pages.
	paths := []string{
		"/gallery", "/upload", "/albums", "/tags",
		"/settings/password", "/settings/2fa", "/settings/account", "/settings/api-keys",
		"/admin", "/admin/users", "/admin/mail", "/admin/files",
	}

	usesAlpine := func(body string) bool {
		for _, marker := range []string{"x-data", "x-model", "x-show", "x-text", "@submit", "@click"} {
			if strings.Contains(body, marker) {
				return true
			}
		}
		return false
	}

	checked := 0
	for _, path := range paths {
		resp, body := h.get(path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
			continue
		}
		checked++

		loads := strings.Contains(body, "alpine.min.js")
		if usesAlpine(body) && !loads {
			t.Errorf("%s uses Alpine attributes but does not load Alpine", path)
		}
	}

	if checked < len(paths) {
		t.Fatalf("only %d of %d pages rendered", checked, len(paths))
	}
}
