// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

// A bare semicolon in a form value used to lose the whole field, because Go's
// form parser refuses to guess whether it was meant as a separator. Refusing to
// guess is right; losing the field is not.

func TestAPasswordWithASemicolonCanBeSignedInWith(t *testing.T) {
	const password = "correct;horse;battery"

	h := newHarness(t)
	h.get("/")

	resp, _ := h.postForm("/register", url.Values{
		"csrf_token": {h.csrf()},
		"username":   {"semicolons"},
		"password":   {password},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("register = %d, want 303: a password with a semicolon in it was refused", resp.StatusCode)
	}
	if resp, _ := h.get("/gallery"); resp.StatusCode != http.StatusOK {
		t.Fatalf("registering did not sign in: %d", resp.StatusCode)
	}

	// Sign out, and back in with the same password. Without the rewrite the
	// field is dropped on the way in, so the password arrives empty and this is
	// a lockout for somebody who chose it perfectly legitimately.
	if resp, _ := h.postForm("/logout", url.Values{"csrf_token": {h.csrf()}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout = %d", resp.StatusCode)
	}

	resp, body := h.postForm("/login", url.Values{
		"csrf_token": {h.csrf()},
		"username":   {"semicolons"},
		"password":   {password},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign in = %d, want 303 (%s)", resp.StatusCode, truncate(body))
	}
	if resp, _ := h.get("/gallery"); resp.StatusCode != http.StatusOK {
		t.Error("signing in did not take")
	}
}

func TestAReportNoteKeepsItsSemicolon(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")
	owner := h.seedUser("owner")
	reporter := h.seedUser("reporter")

	fileID := uploadAs(t, h.sessionFor(t, owner.ID), "photo.png", "public")

	const note = "spam; and there is a lot of it"
	resp, _ := h.sessionFor(t, reporter.ID).post("/reports", url.Values{
		"target_kind": {"file"},
		"target_id":   {fileID},
		"reason":      {"spam"},
		"note":        {note},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("report = %d", resp.StatusCode)
	}

	reports, _, err := h.store.ListReports(t.Context(), models.ReportOpen, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("%d reports, want 1", len(reports))
	}
	if reports[0].Note != note {
		t.Errorf("note = %q, want %q", reports[0].Note, note)
	}
}

func TestASearchTermKeepsItsSemicolon(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	// A file whose name contains one, so the filter can be seen working rather
	// than merely echoed back.
	resp, body := h.uploadFiles(map[string]string{}, []uploadFile{
		{name: "half;semi.png", data: pngFixture(t, 32, 32)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d (%s)", resp.StatusCode, truncate(body))
	}
	id := firstFileID(t, body)

	// Encoded, as a link would carry it.
	if _, page := h.get("/gallery?q=half%3Bsemi"); !strings.Contains(page, id) {
		t.Error("searching for the encoded term did not find the file")
	}
	// And bare, which the rewrite turns into the same thing.
	if _, page := h.get("/gallery?q=half;semi"); !strings.Contains(page, id) {
		t.Error("searching for a bare semicolon did not find the file")
	}
	// The box carries the term back, rather than appearing to have been cleared.
	if _, page := h.get("/gallery?q=half%3Bsemi"); !strings.Contains(page, `value="half;semi"`) {
		t.Error("the search box does not carry the term")
	}
	// A term that matches nothing still shows nothing, so the filter is a filter.
	if _, page := h.get("/gallery?q=nothingmatchesthis"); strings.Contains(page, id) {
		t.Error("an unrelated search returned the file")
	}
}

func TestMultipartBodiesAreLeftAlone(t *testing.T) {
	// The rewrite is for ordinary form submissions. A multipart body carries
	// files and must be delivered untouched, which the whole upload suite
	// depends on; this checks the middleware declines to touch it at all.
	server := &Server{}
	seen := ""
	handler := server.semicolonMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = string(body)
	}))

	payload := "boundary;still here"
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(payload))
	req.Header.Set("Content-Type", `multipart/form-data; boundary="x"; charset=utf-8`)

	handler.ServeHTTP(httptest.NewRecorder(), req)
	if seen != payload {
		t.Errorf("a multipart body was rewritten: %q", seen)
	}
}

func TestAnOversizedFormBodyIsDeliveredUnchanged(t *testing.T) {
	// Past the limit the middleware cannot be sure it has seen the whole thing,
	// so it hands the body on rather than rewriting half of it.
	server := &Server{}
	var received int
	handler := server.semicolonMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the body: %v", err)
		}
		received = len(body)
	}))

	value := strings.Repeat("x;", formRewriteLimit)
	body := "big=" + value

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	handler.ServeHTTP(httptest.NewRecorder(), req)
	if received != len(body) {
		t.Errorf("received %d bytes, want %d: the body was truncated", received, len(body))
	}
}

func TestTheRewriteOnlyTouchesWhatItMust(t *testing.T) {
	server := &Server{}

	var got string
	handler := server.semicolonMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = fmt.Sprintf("%s|%s|%s", r.FormValue("a"), r.FormValue("b"), r.FormValue("c"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("a=one&b=two;three&c=four"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if got != "one|two;three|four" {
		t.Errorf("parsed %q, want \"one|two;three|four\"", got)
	}

	// An already-encoded semicolon is not double-encoded.
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("a=already%3Bencoded"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if got != "already;encoded||" {
		t.Errorf("parsed %q, want \"already;encoded||\"", got)
	}
}
