// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"imvault/internal/db"
	"imvault/internal/instance"
	"imvault/internal/maintenance"
	"imvault/internal/secrets"
	"imvault/internal/testutil"
)

func TestMaintenanceCommandsHelpAndArguments(t *testing.T) {
	adminTestEnvironment(t)
	for _, command := range []string{"backup", "restore", "migrate-storage", "rebuild-thumbnails"} {
		if err := runCommand([]string{command, "--help"}, nil, io.Discard); err != nil {
			t.Fatal(command, err)
		}
		if err := runCommand([]string{command, "--unknown"}, nil, io.Discard); err == nil {
			t.Fatal("unknown flag accepted", command)
		}
		if err := runCommand([]string{command, "extra"}, nil, io.Discard); err == nil {
			t.Fatal("extra argument accepted", command)
		}
	}
}

func TestScheduledBackupCommandDefaultsToSevenAndAllowsExplicitRetention(t *testing.T) {
	path := adminTestEnvironment(t)
	database, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, database)
	if _, err := secrets.Load(filepath.Join(filepath.Dir(path), "secret.key"), ""); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(t.TempDir(), "snapshots")
	for i := 0; i < 9; i++ {
		if err := runCommand([]string{"backup", "--output-dir", parent}, nil, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if got := commandBackups(t, parent); len(got) != 7 {
		t.Fatalf("default kept %d backups, want seven", len(got))
	}
	if err := runCommand([]string{"backup", "--output-dir", parent, "--keep", "0"}, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := commandBackups(t, parent); len(got) != 8 {
		t.Fatal("--keep 0 removed backups", got)
	}
	if err := runCommand([]string{"backup", "--output-dir", parent, "--keep", "2"}, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, backup := range commandBackups(t, parent) {
		if err := maintenance.Restore(t.Context(), backup, filepath.Join(t.TempDir(), "restore"), io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if got := commandBackups(t, parent); len(got) != 2 {
		t.Fatal("explicit count ignored", got)
	}
	for _, args := range [][]string{{"backup", "--output-dir", parent, "--keep", "-1"}, {"backup", "--output", filepath.Join(t.TempDir(), "manual"), "--keep", "2"}} {
		if err := runCommand(args, nil, io.Discard); err == nil {
			t.Fatal("invalid retention arguments accepted", args)
		}
	}
}

func commandBackups(t *testing.T, parent string) []string {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			paths = append(paths, filepath.Join(parent, entry.Name()))
		}
	}
	return paths
}

func TestBackupCommandCoexistsWithServerAndRestoresWithoutSourceConfig(t *testing.T) {
	path := adminTestEnvironment(t)
	database, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, database)
	if _, err := secrets.Load(filepath.Join(filepath.Dir(path), "secret.key"), ""); err != nil {
		t.Fatal(err)
	}
	server, err := instance.AcquireServer(path)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot")
	args := []string{"backup", "--output", backup}
	if err := runCommand(args, nil, io.Discard); err != nil {
		t.Fatal("live backup refused", err)
	}
	testutil.Close(t, server)
	t.Setenv("IMVAULT_STORAGE", "s3") // Intentionally incomplete and unavailable.
	t.Setenv("IMVAULT_S3_BUCKET", "")
	output := filepath.Join(t.TempDir(), "restored")
	if err := runCommand([]string{"restore", "--input", backup, "--output", output}, nil, io.Discard); err != nil {
		t.Fatal("restore depended on source service configuration", err)
	}
}
