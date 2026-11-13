// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"github.com/airencracken/comfylib/problem"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"imvault/internal/models"
	"imvault/internal/store"
)

// styledFailure reports whether a body is the app's error page rather than
// net/http's bare text.
func styledFailure(t *testing.T, body, message string) {
	t.Helper()
	if !strings.Contains(body, `<p class="code">`) || !strings.Contains(body, "<h1>"+html.EscapeString(message)+"</h1>") {
		t.Fatalf("not the styled error page with %q: %s", message, truncate(body))
	}
}

func TestBrowserFailuresUseTheStyledErrorPage(t *testing.T) {
	h := newHarness(t)
	alice, bob := h.seedUser("alice"), h.seedUser("bob")
	owner := h.sessionFor(t, alice.ID)
	album, err := h.store.CreateAlbum(t.Context(), alice.ID, store.AlbumInput{Title: "Trip", Visibility: models.VisibilityMembers})
	if err != nil {
		t.Fatal(err)
	}
	id := h.uploadOne(h.seedKey(alice.ID, "test", nil), "photo.png", 20, 20)

	resp, body := owner.post("/f/"+id+"/tags/not-a-number/delete", url.Values{})
	if resp.StatusCode == http.StatusBadRequest {
		styledFailure(t, body, "That tag doesn't exist.")
	}
	resp, body = owner.post("/f/"+id+"/rename", url.Values{"name": {"../../etc/passwd"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("rename with a path = %d", resp.StatusCode)
	}
	styledFailure(t, body, "Use a filename of 1 to 200 characters, without slashes or control characters.")
	if strings.Contains(body, "etc/passwd") {
		t.Error("the rejected filename was echoed back")
	}

	resp, body = h.sessionFor(t, bob.ID).post("/a/"+album.Slug+"/delete", url.Values{})
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("another member deleted the album: %d", resp.StatusCode)
	}
	if !strings.Contains(body, `<p class="code">`) {
		t.Fatalf("refusal is not the styled page: %s", truncate(body))
	}

	// A missing or forged CSRF token gets the same page, in plain words.
	req, _ := http.NewRequest(http.MethodPost, h.server.URL+"/f/"+id+"/rename", strings.NewReader("name=x.png&csrf_token=forged"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = owner.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	mustClose(t, resp.Body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("forged CSRF = %d", resp.StatusCode)
	}
	styledFailure(t, string(raw), problem.ExpiredMessage)
	if strings.Contains(string(raw), "forged") || strings.Contains(string(raw), "CSRF token") {
		t.Error("the CSRF refusal leaks the mechanism or echoes the token")
	}
}

func TestDiscussionSetupDetailGoesToTheLogNotThePage(t *testing.T) {
	h := newHarness(t)
	var logs bytes.Buffer
	h.srv.log = slog.New(slog.NewTextHandler(&logs, nil))
	owner := h.seedUser("alice")
	session := h.sessionFor(t, owner.ID)
	album, err := h.store.CreateAlbum(t.Context(), owner.ID, store.AlbumInput{Title: "Trip", Visibility: models.VisibilityPublic})
	if err != nil {
		t.Fatal(err)
	}
	h.srv.cfg.WitmootURL = ""
	resp, body := session.post("/a/"+album.Slug+"/share", url.Values{})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("share without configuration = %d", resp.StatusCode)
	}
	styledFailure(t, body, "Discussions aren't set up on this site yet.")
	if strings.Contains(body, "IMVAULT_") {
		t.Error("operator configuration was shown to a member")
	}
	if !strings.Contains(logs.String(), "IMVAULT_WITMOOT_URL") {
		t.Error("the operator detail was not logged")
	}
}

func TestJSONAPIErrorsStayJSON(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	key := h.seedKey(alice.ID, "test", nil)
	resp, raw := h.apiJSON(http.MethodPatch, "/api/v1/files/missing", key, map[string]any{"description": "x"})
	if resp.StatusCode != http.StatusNotFound || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") || bytes.Contains(raw, []byte("<html")) {
		t.Fatalf("API error changed shape: %d %s %s", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
	}
}

// Every bare http.Error left in a handler file is one of the fallbacks used
// when the error page itself cannot be drawn, or the plain health check.
func TestOnlyFallbacksUseBareHTTPError(t *testing.T) {
	allowed := map[string]int{"render.go": 2, "handlers_admin_rows.go": 2, "middleware.go": 1, "server.go": 1}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`http\.Error\(`)
	lowerStart := regexp.MustCompile(`s\.fail\(w, r, [^,]+, "[a-z]`)
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(pattern.FindAll(source, -1)); got != allowed[name] {
			t.Errorf("%s has %d bare http.Error calls, want %d", name, got, allowed[name])
		}
		if lowerStart.Match(source) {
			t.Errorf("%s has an error message that is not in sentence case", name)
		}
	}
}
