// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestPageHeadingsAvoidDecorativeEyebrows(t *testing.T) {
	err := fs.WalkDir(templatesFS, "templates", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		data, err := fs.ReadFile(templatesFS, path)
		if err == nil && regexp.MustCompile(`class="[^"]*\beyebrow\b`).Match(data) {
			t.Errorf("%s adds a decorative label above its heading", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Check the public form contract on rendered pages, including validation errors
// and HTMX replacements. CSS and keyboard behavior are checked in the browser.
func assertInvitationPageSchema(t *testing.T, body string, full bool) {
	t.Helper()
	for _, marker := range []string{`class="invitation-layout"`, `aria-labelledby="create-invitation"`, `aria-labelledby="existing-invitations"`, `<h2 id="create-invitation">Create an invitation</h2>`, `<h2 id="existing-invitations">Existing invitations</h2>`} {
		if strings.Count(body, marker) != 1 {
			t.Errorf("invitation page needs exactly one %s", marker)
		}
	}
	if full && (strings.Count(body, "<h1>Invitations</h1>") != 1 || !strings.Contains(body, `class="invitation-page"`)) {
		t.Error("missing single invitation page heading")
	}
	if strings.Contains(body, "<table") || regexp.MustCompile(`class="[^"]*\beyebrow\b`).MatchString(body) {
		t.Error("invitation page uses a wide table or decorative eyebrow")
	}
	for _, id := range []string{"invite-label", "invite-uses", "invite-days"} {
		if strings.Count(body, `for="`+id+`"`) != 1 {
			t.Errorf("%s needs an associated label", id)
		}
		input := regexp.MustCompile(`<input\b[^>]*\bid="` + id + `"[^>]*>`).FindString(body)
		if input == "" || !strings.Contains(input, `name="`) {
			t.Errorf("%s is missing its named input", id)
		}
		if id != "invite-label" && (!strings.Contains(input, `type="number"`) || !strings.Contains(input, `min="`) || !strings.Contains(input, `max="`) || !strings.Contains(input, `aria-describedby="`+id+`-help"`)) {
			t.Errorf("%s is missing its numeric bounds or help association", id)
		}
	}
	ids := map[string]bool{}
	for _, match := range regexp.MustCompile(`\bid="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if ids[match[1]] {
			t.Errorf("duplicate element id %q", match[1])
		}
		ids[match[1]] = true
	}
}
