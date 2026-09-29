package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOpenRCInstallProtectsAndPreservesCredentials(t *testing.T) {
	dest := t.TempDir()
	run := func() {
		t.Helper()
		cmd := exec.Command("make", "-C", "..", "install-openrc", "DESTDIR="+dest)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("install: %v: %s", err, out)
		}
	}
	run()
	path := filepath.Join(dest, "etc/conf.d/imvault")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new configuration is not mode 0600: %v", err)
	}
	custom := "IMVAULT_SMTP_PASSWORD=local-placeholder\n"
	if err := os.WriteFile(path, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	run()
	content, err := os.ReadFile(path)
	if err != nil || string(content) != custom {
		t.Fatal("reinstallation changed local credentials")
	}
	info, err = os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("reinstallation did not tighten old configuration permissions")
	}
}
