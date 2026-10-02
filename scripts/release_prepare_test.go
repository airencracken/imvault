// SPDX-License-Identifier: AGPL-3.0-or-later

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The packaged unit is the contrib unit with only the binary path changed: the
// data directory mode now comes from the unit itself, not a release-time edit.
func TestReleasePreparationPackagesTheUnitAsWritten(t *testing.T) {
	if testing.Short() {
		t.Skip("lists dependencies for two platforms")
	}
	out := t.TempDir()
	cmd := exec.Command("sh", "release/prepare.sh")
	cmd.Env = append(os.Environ(), "RELEASE_DIR="+out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prepare: %s (%v)", output, err)
	}
	unit, err := os.ReadFile(filepath.Join(out, "imvault.service"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("../contrib/systemd/imvault.service")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(string(source), "/usr/local/bin/imvault", "/usr/bin/imvault"); string(unit) != want {
		t.Fatalf("packaged unit differs from contrib beyond the binary path:\n%s", unit)
	}
	for _, line := range []string{"ExecStart=/usr/bin/imvault\n", "StateDirectoryMode=0700\n", "UMask=0077\n"} {
		if !strings.Contains(string(unit), line) {
			t.Errorf("packaged unit lacks %q", line)
		}
	}
	env, err := os.ReadFile(filepath.Join(out, "imvault.env"))
	if err != nil || strings.Count(string(env), "\nIMVAULT_ADDR=") != 1 || !strings.Contains(string(env), "IMVAULT_ADDR=127.0.0.1:8080\n") {
		t.Fatalf("packaged environment does not bind to loopback once: %v\n%s", err, env)
	}
	for _, name := range []string{"THIRD_PARTY_NOTICES.txt", "copyright"} {
		if info, err := os.Stat(filepath.Join(out, name)); err != nil || info.Size() == 0 {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
	// The script carries no release-time mode edits any more.
	script, err := os.ReadFile("release/prepare.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "StateDirectoryMode") {
		t.Error("prepare.sh still rewrites StateDirectoryMode")
	}
}
