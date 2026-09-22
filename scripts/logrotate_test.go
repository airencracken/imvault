// SPDX-License-Identifier: AGPL-3.0-or-later

package scripts_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLogrotateInstallPreservesLocalRules(t *testing.T) {
	rule, err := os.ReadFile("../contrib/logrotate/imvault")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"install-logrotate", "install-openrc"} {
		t.Run(target, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "stage with spaces")
			run := func() {
				t.Helper()
				cmd := exec.Command("make", "-C", "..", target, "DESTDIR="+dest, "PREFIX=/usr")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("install: %v: %s", err, out)
				}
			}
			run()
			path := filepath.Join(dest, "etc/logrotate.d/imvault")
			installed, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(installed, rule) {
				t.Fatalf("installed rule differs: %v", err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o644 {
				t.Fatal("rule must be readable but not writable by other users")
			}
			custom := []byte("# locally managed rule\n")
			if err := os.WriteFile(path, custom, 0o644); err != nil {
				t.Fatal(err)
			}
			run()
			installed, err = os.ReadFile(path)
			if err != nil || !bytes.Equal(installed, custom) {
				t.Fatal("reinstall overwrote the local rule")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("managed-elsewhere", path); err != nil {
				t.Fatal(err)
			}
			run()
			if got, err := os.Readlink(path); err != nil || got != "managed-elsewhere" {
				t.Fatal("reinstall overwrote a local symlink")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("reinstall followed a dangling symlink")
			}
		})
	}
}

func TestLogrotateInstallSupportsCustomConfigDirectory(t *testing.T) {
	dest := t.TempDir()
	cmd := exec.Command("make", "-C", "..", "install-logrotate", "DESTDIR="+dest, "LOGROTATEDIR=/custom config/rotation")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("custom install: %v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dest, "custom config/rotation/imvault")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "etc")); !os.IsNotExist(err) {
		t.Fatal("custom install also wrote the default config path")
	}
}
