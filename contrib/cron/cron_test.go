// SPDX-License-Identifier: AGPL-3.0-or-later
package cron_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionalCronUsesServiceAwareVerifiedBackup(t *testing.T) {
	data, err := os.ReadFile("imvault-backup")
	if err != nil {
		t.Fatal(err)
	}
	jobs := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, "=") {
			continue
		}
		jobs = append(jobs, line)
	}
	if len(jobs) != 1 {
		t.Fatalf("cron jobs=%v", jobs)
	}
	fields := strings.Fields(jobs[0])
	if len(fields) != 12 || fields[5] != "root" || strings.Join(fields[6:], " ") != "/usr/bin/imvault backup --output-dir /var/backups/imvault --keep 7" {
		t.Fatalf("cron does not use the verified service command: %v", fields)
	}
	if strings.Contains(jobs[0], "%") || strings.Contains(jobs[0], " stop") {
		t.Fatal("cron percent expansion or service interruption")
	}
}

func TestCronInstallationIsOptInAndPreservesLocalConfiguration(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	run := func(target string) {
		t.Helper()
		command := exec.Command("make", "--no-print-directory", target, "DESTDIR="+destination, "PREFIX=/opt/photos")
		command.Dir = root
		command.Env = append(os.Environ(), "MAKEFLAGS=", "MAKEOVERRIDES=")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", target, err, output)
		}
	}
	run("install-systemd")
	job := filepath.Join(destination, "etc/cron.d/imvault-backup")
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatal("ordinary installation enabled cron backups", err)
	}
	run("install-backup-cron")
	contents, err := os.ReadFile(job)
	if err != nil || !strings.Contains(string(contents), "root /opt/photos/bin/imvault backup --output-dir /var/backups/imvault --keep 7") {
		t.Fatal("installed cron has the wrong prefix", err)
	}
	if err := os.WriteFile(job, []byte("# local schedule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("install-backup-cron")
	contents, err = os.ReadFile(job)
	if err != nil || string(contents) != "# local schedule\n" {
		t.Fatal("reinstallation overwrote local schedule", err)
	}
}
