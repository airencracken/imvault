// SPDX-License-Identifier: AGPL-3.0-or-later

package scripts_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The data directory holds account credentials, the encryption key and
// private uploads. Every file that creates it or tells an operator how to,
// gives it mode 0700.
func TestEveryDataDirectoryModeIsPrivate(t *testing.T) {
	dataDir := regexp.MustCompile(`/var/lib/imvault|IMVAULT_DATA_DIR|\s/data\b|ACCT_USER_HOME_PERMS|StateDirectoryMode`)
	mode := regexp.MustCompile(`(?:-m ?|--mode |chmod |fperms |PERMS=|Mode=)0?([0-7]{3})\b`)
	found := 0
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "bin", "dist", ".release", "__pycache__", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".pyc") || strings.HasSuffix(path, ".png") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			// A command continued onto the next line names its path there.
			joined := line
			if strings.HasSuffix(strings.TrimSpace(line), "\\") && i+1 < len(lines) {
				joined += lines[i+1]
			}
			if !dataDir.MatchString(joined) {
				continue
			}
			for _, match := range mode.FindAllStringSubmatch(line, -1) {
				found++
				if match[1] != "700" {
					t.Errorf("%s:%d gives the data directory mode %s: %s", path, i+1, match[1], strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The unit, OpenRC script, Debian postinst, Alpine and Gentoo recipes,
	// Dockerfile and the documented manual step.
	if found < 7 {
		t.Fatalf("found only %d data directory modes; the scan has stopped seeing them", found)
	}
}
