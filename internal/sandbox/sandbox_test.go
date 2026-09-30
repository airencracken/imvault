// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestServicePolicyKeepsSecretsOutOfArguments(t *testing.T) {
	data := t.TempDir()
	args, env, err := (Service{Prefix: "TEST_", DataDir: data, Executable: "/usr/bin/true", Env: []string{
		"TEST_SMTP_PASSWORD=secret-value", "TEST_DATA_DIR=wrong", "AWS_SECRET_ACCESS_KEY=unrelated", "LD_PRELOAD=host-loader", "TMPDIR=/host/tmp",
	}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	for _, required := range []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--cap-drop", "--new-session", "--tmpfs", "--ro-bind", data} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing confinement: %s", required)
		}
	}
	for _, forbidden := range []string{"secret-value", "--unshare-net", "--die-with-parent", "--ro-bind\n/\n/"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("unsafe service policy contains %s", forbidden)
		}
	}
	joined = strings.Join(env, "\n")
	for _, required := range []string{"TEST_SMTP_PASSWORD=secret-value", "TEST_DATA_DIR=" + data, "TMPDIR=/tmp"} {
		if !strings.Contains(joined, required) {
			t.Errorf("environment missing %s", required)
		}
	}
	for _, forbidden := range []string{"LD_PRELOAD", "AWS_SECRET", "=wrong", "/host/tmp"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("environment leaks %s", forbidden)
		}
	}
}

func TestWritableDirectoriesRejectBroadAndAdversarialPaths(t *testing.T) {
	for _, path := range []string{"/", "/var", "/var/lib", "/home", "/root", "/srv", "/tmp", "/usr", "/usr/local", "/etc", "/proc", "/dev", "/app", "relative", "/var/lib/../lib", "/var/lib/evil\nname", "/var/lib/evil\x00name"} {
		if _, err := writableDir(path); err == nil {
			t.Errorf("accepted unsafe directory %q", path)
		}
	}
	root := t.TempDir()
	link := filepath.Join(root, "looks-safe")
	if err := os.Symlink("/", link); err != nil {
		t.Fatal(err)
	}
	if _, err := writableDir(link); err == nil {
		t.Fatal("symlink exposed the entire host")
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(root, "missing")} {
		if _, err := writableDir(path); err == nil {
			t.Fatalf("accepted non-directory %s", path)
		}
	}
	for _, name := range []string{"space and & punctuation", "--option-looking", "日本語"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if got, err := writableDir(path); err != nil || got != path {
			t.Fatalf("safe directory %q: %s %v", path, got, err)
		}
	}
}

func TestMediaBaseHasNoNetworkOrHostEnvironment(t *testing.T) {
	args, err := Base(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--unshare-net", "--die-with-parent", "--unshare-pid"} {
		if !strings.Contains(strings.Join(args, "\n"), want) {
			t.Fatal("missing " + want)
		}
	}
	if strings.Contains(strings.Join(RuntimeEnv(), "\n"), "PASSWORD") {
		t.Fatal("media environment contains credentials")
	}
}

func TestSandboxSetupFailureIsAnError(t *testing.T) {
	if err := Check(context.Background(), "/does-not-exist", nil, nil); err == nil {
		t.Fatal("missing Bubblewrap accepted")
	}
	if err := Check(context.Background(), "/usr/bin/false", nil, nil); err == nil {
		t.Fatal("failed confinement accepted")
	}
}

func TestReadMountsMustBeExistingFiles(t *testing.T) {
	for _, path := range []string{t.TempDir(), "/missing-file", "relative", "/tmp/file\nname"} {
		_, _, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", ReadFiles: []string{path}}).Policy()
		if err == nil {
			t.Errorf("accepted read mount %q", path)
		}
	}
}

func TestRealBubblewrapMediaBoundary(t *testing.T) {
	if os.Getenv("COMFYWARE_SANDBOX_TEST") != "1" {
		t.Skip("set COMFYWARE_SANDBOX_TEST=1 to require real Linux namespace tests")
	}
	binary, err := exec.LookPath("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	args, err := Base(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), binary, args, RuntimeEnv()); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "host-secret")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, append(args, "--", "/bin/sh", "-c", `test ! -e "$1" && test ! -e /etc/shadow && test -z "$TEST_PASSWORD" && test ! -w /usr && test "$(ls /sys/class/net 2>/dev/null)" = ""`, "check", secret)...)
	cmd.Env = RuntimeEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("media confinement failed: %v: %s", err, out)
	}
	// Inspect the namespace instead of merely relying on an absent sysfs mount.
	cmd = exec.Command(binary, append(args, "--", "/bin/sh", "-c", `test "$(cat /proc/net/dev | wc -l)" -eq 3`)...)
	cmd.Env = RuntimeEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("media network namespace is not isolated: %v: %s", err, out)
	}
}

func TestSetuidBubblewrapIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bwrap")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	if _, err := Binary(path); err == nil {
		t.Fatal("setuid launcher accepted")
	}
}
