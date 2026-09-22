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

func TestInstallWarnsWhenEitherVideoToolIsMissing(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Fatal("make is required to test installation")
	}
	for _, tc := range []struct {
		name   string
		tools  []string
		staged bool
		warn   bool
	}{
		{"neither", nil, false, true},
		{"ffmpeg only", []string{"ffmpeg"}, false, true},
		{"ffprobe only", []string{"ffprobe"}, false, true},
		{"both", []string{"ffmpeg", "ffprobe"}, false, false},
		{"packaging", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			// Exercise the install recipe with an isolated PATH. Build and copy
			// are stand-ins; the dependency check is the actual install script.
			for _, tool := range append([]string{"go", "install"}, tc.tools...) {
				if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("/bin/sh", filepath.Join(bin, "sh")); err != nil {
				t.Fatal(err)
			}
			args := []string{"-C", "..", "install", "PREFIX=" + t.TempDir(), "VERSION=test"}
			if tc.staged {
				args = append(args, "DESTDIR="+t.TempDir())
			}
			cmd := exec.Command(makePath, args...)
			cmd.Env = append(os.Environ(), "PATH="+bin, "IMVAULT_FFMPEG=ffmpeg", "IMVAULT_FFPROBE=ffprobe")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("install: %v: %s", err, out)
			}
			if strings.Contains(string(out), "Warning: missing video tools:") != tc.warn {
				t.Fatalf("warning=%t, output: %s", tc.warn, out)
			}
			if tc.warn && !strings.Contains(string(out), "emerge --ask media-video/ffmpeg") {
				t.Fatal("warning has no Gentoo installation advice")
			}
		})
	}
}
