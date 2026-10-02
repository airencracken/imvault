// SPDX-License-Identifier: AGPL-3.0-or-later

package openrc_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDataDirectoryReachesDaemon(t *testing.T) {
	for _, override := range []string{"", "/srv/imvault data"} {
		t.Run(override, func(t *testing.T) {
			cmd := exec.Command("sh", "-c", `
if [ -n "$1" ]; then IMVAULT_DATA_DIR=$1; fi
. ./imvault || exit 1
checkpath() { return 0; }
start_pre || exit 1
sh -c 'printf "%s" "$IMVAULT_DATA_DIR"'
`, "test", override)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "RC_SVCNAME=imvault-audit-no-config"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("start_pre: %v: %s", err, out)
			}
			want := override
			if want == "" {
				want = "/var/lib/imvault"
			}
			if strings.TrimSpace(string(out)) != want {
				t.Fatalf("daemon data directory = %q, want %q", out, want)
			}
		})
	}
}

// A relative data directory is refused before checkpath can create and chown
// it relative to wherever the init system happens to be.
func TestRelativeDataDirectoryIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	cmd := exec.Command("sh", "-c", `
IMVAULT_DATA_DIR=relative/imvault
. ./imvault || exit 1
checkpath() { echo "checkpath $*"; return 0; }
eerror() { echo "error: $*"; }
start_pre
echo "status $?"
`)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "RC_SVCNAME=imvault-audit-no-config"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shell: %v: %s", err, out)
	}
	if strings.Contains(string(out), "checkpath") {
		t.Fatalf("checkpath ran for a relative path:\n%s", out)
	}
	if !strings.Contains(string(out), "status 1") || !strings.Contains(string(out), "absolute path") {
		t.Fatalf("a relative data directory was not refused:\n%s", out)
	}
}
