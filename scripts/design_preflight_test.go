// SPDX-License-Identifier: AGPL-3.0-or-later
package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesignMutationGatePrimesColdCacheBeforeOfflineTests(t *testing.T) {
	makeBinary, err := exec.LookPath("make")
	if err != nil {
		t.Skip("requires make")
	}
	makefile, err := filepath.Abs("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	for _, failDownload := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold-cache", true: "failed-download"}[failDownload], func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "downloaded")
			ran := filepath.Join(dir, "mutations-ran")
			fakeGo := `#!/bin/sh
[ "$GOWORK" = off ] || exit 7
[ "$*" = 'mod download' ] || exit 8
[ "$FAIL_DOWNLOAD" = false ] || exit 9
: > "$PREFLIGHT_MARKER" || exit 10
`
			fakePython := `#!/bin/sh
[ "$GOWORK" = off ] || exit 11
[ -f "$PREFLIGHT_MARKER" ] || exit 12
[ "$*" = 'scripts/mutate.py scripts/mutations/design.json' ] || exit 13
: > "$MUTATIONS_MARKER" || exit 14
`
			for name, contents := range map[string]string{"go": fakeGo, "python3": fakePython} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(makeBinary, "--no-print-directory", "-f", makefile, "test-design-mutations")
			cmd.Dir = dir
			// A minimal PATH prevents an accidental fallback to real downloads or
			// the developer's already-populated module cache.
			cmd.Env = append(os.Environ(), "PATH="+dir, "GOWORK=auto", "PREFLIGHT_MARKER="+marker, "MUTATIONS_MARKER="+ran, "FAIL_DOWNLOAD="+map[bool]string{false: "false", true: "true"}[failDownload])
			out, err := cmd.CombinedOutput()
			_, ranErr := os.Stat(ran)
			if failDownload {
				if err == nil || !os.IsNotExist(ranErr) {
					t.Fatalf("offline tests ran after a failed download: %s", out)
				}
			} else if err != nil || ranErr != nil || !strings.Contains(string(out), "go mod download") {
				t.Fatalf("cold-cache preflight did not precede offline tests: %v, %s", err, out)
			}
		})
	}
}
