// SPDX-License-Identifier: AGPL-3.0-or-later
package scripts_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The actual terminal regression runner is shared, just like the prompt code.
func TestPasswordCLIRunnerMatchesPinnedComfylib(t *testing.T) {
	_, dir := pinnedComfylib(t)
	upstream, err := os.ReadFile(filepath.Join(dir, "tools", "check_password_cli.py"))
	if err != nil {
		t.Fatal(err)
	}
	local, err := os.ReadFile("check_password_cli.py")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(local, upstream) {
		t.Fatal("copy tools/check_password_cli.py from the pinned Comfylib release")
	}
}
