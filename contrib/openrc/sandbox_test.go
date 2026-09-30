// SPDX-License-Identifier: AGPL-3.0-or-later

package openrc_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSandboxIsExplicitAndInvalidModesFailBeforeWrites(t *testing.T) {
	for _, mode := range []string{"", "false", "true", "auto", "true; echo injected"} {
		cmd := exec.Command("sh", "-c", `
. ./imvault || exit 1
eerror() { printf '%s\n' "$*"; }
checkpath() { echo WRITE; }
start_pre || exit 1
printf 'ARGS=%s\n' "$command_args"
`, "test")
		cmd.Env = []string{"PATH=/usr/bin:/bin", "RC_SVCNAME=imvault-sandbox-test", "IMVAULT_SANDBOX=" + mode}
		out, err := cmd.CombinedOutput()
		if mode == "auto" || strings.Contains(mode, ";") {
			if err == nil || !strings.Contains(string(out), "must be true or false") || strings.Contains(string(out), "WRITE") {
				t.Fatalf("invalid mode %s: %s %v", mode, out, err)
			}
			continue
		}
		want := "ARGS=\n"
		if mode == "true" {
			want = "ARGS=sandbox\n"
		}
		if err != nil || !strings.HasSuffix(string(out), want) {
			t.Fatalf("mode %s: %s %v", mode, out, err)
		}
	}
}
