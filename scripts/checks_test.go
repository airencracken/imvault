// SPDX-License-Identifier: AGPL-3.0-or-later

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakeDoesNotHideJavaScriptFailures(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is unavailable")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\nexit 9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"test-js", "check-js", "test-browser"} {
		cmd := exec.Command(makePath, "-C", "..", target)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err == nil || strings.Contains(string(out), "not installed") {
			t.Errorf("%s hid failure: err=%v, output=%s", target, err, out)
		}
	}
}

func TestSystemdInstallUsesBinaryPrefix(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is unavailable")
	}
	for _, prefix := range []string{"/usr", "/usr/local"} {
		dest := t.TempDir()
		cmd := exec.Command(makePath, "-C", "..", "install-systemd", "DESTDIR="+dest, "PREFIX="+prefix)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("stage unit: %v: %s", err, out)
		}
		unit, err := os.ReadFile(filepath.Join(dest, "etc/systemd/system/imvault.service"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(unit), "ExecStart="+prefix+"/bin/imvault\n") {
			t.Errorf("unit does not use prefix %s", prefix)
		}
	}
}
