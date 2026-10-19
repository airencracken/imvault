// SPDX-License-Identifier: AGPL-3.0-or-later
package scripts

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseGuideMatchesNotesAndDownloadExamples(t *testing.T) {
	guide, err := os.ReadFile("../docs/releases.md")
	if err != nil {
		t.Fatal(err)
	}
	latest := regexp.MustCompile(`The latest release is \[([0-9]+\.[0-9]+\.[0-9]+)\]\(release-notes/([0-9]+\.[0-9]+\.[0-9]+)\.md\)`).FindStringSubmatch(string(guide))
	if len(latest) != 3 || latest[1] != latest[2] {
		t.Fatal("latest version and release-note link disagree")
	}
	notes, err := os.ReadFile(filepath.Join("../docs/release-notes", latest[1]+".md"))
	if err != nil || !strings.HasPrefix(string(notes), "# Imvault "+latest[1]+"\n") {
		t.Fatal("release notes missing or titled for a different release", err)
	}
	for _, pattern := range []string{`release_version=([0-9]+\.[0-9]+\.[0-9]+)`, `imvault_([0-9]+\.[0-9]+\.[0-9]+)_linux_amd64`} {
		matches := regexp.MustCompile(pattern).FindAllStringSubmatch(string(guide), -1)
		if len(matches) == 0 {
			t.Fatal("missing installation example", pattern)
		}
		for _, match := range matches {
			if match[1] != latest[1] {
				t.Fatalf("example uses %s, latest is %s", match[1], latest[1])
			}
		}
	}
	normalizedNotes := strings.Join(strings.Fields(strings.ToLower(string(notes))), " ")
	for _, migration := range regexp.MustCompile(`\b[0-9]{3}_[a-z_]+\.sql\b`).FindAllString(string(notes), -1) {
		if _, err := os.Stat(filepath.Join("../internal/db/migrations", migration)); err != nil {
			t.Fatal("unknown migration in release notes", migration, err)
		}
		for _, required := range []string{"before upgrading", "older binaries", "pre-upgrade"} {
			if !strings.Contains(normalizedNotes, required) {
				t.Fatal("migration notes must explain backups and downgrades", required)
			}
		}
	}
}
