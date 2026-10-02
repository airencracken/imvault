// SPDX-License-Identifier: AGPL-3.0-or-later

package scripts_test

import (
	"os"
	"strings"
	"testing"
)

// The package gate's systemctl and md5sum calls stop the test when they fail,
// instead of comparing an empty answer or building a package without sums.
func TestPackageGateChecksItsOwnCommands(t *testing.T) {
	script, err := os.ReadFile("release/test-deb.sh")
	if err != nil {
		t.Fatal(err)
	}
	for number, line := range strings.Split(string(script), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "#") {
			continue
		}
		for _, command := range []string{"systemctl show", "md5sum"} {
			if !strings.Contains(code, command) {
				continue
			}
			// A command substitution inside [ ] discards the exit status.
			if strings.Contains(code, "[ \"$("+command) {
				t.Errorf("line %d tests %s output without checking its status: %s", number+1, command, code)
			}
			if !strings.Contains(code, "|| exit 1") && !strings.Contains(code, "|| fail ") {
				t.Errorf("line %d ignores a failing %s: %s", number+1, command, code)
			}
		}
	}
}
