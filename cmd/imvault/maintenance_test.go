// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"imvault/internal/db"
	"imvault/internal/instance"
	"imvault/internal/secrets"
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

func TestBackupCommandRequiresStoppedInstanceAndRestoresWithoutSourceConfig(t *testing.T) {
	path := adminTestEnvironment(t)
	database, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	database.Close()
	if _, err := secrets.Load(filepath.Join(filepath.Dir(path), "secret.key"), ""); err != nil {
		t.Fatal(err)
	}
	server, err := instance.AcquireServer(path)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot")
	args := []string{"backup", "--output", backup}
	if err := runCommand(args, nil, io.Discard); err == nil || !strings.Contains(err.Error(), "stop imvault") {
		t.Fatal("backup did not reject running server", err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatal("blocked backup changed output")
	}
	server.Close()
	if err := runCommand(args, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMVAULT_STORAGE", "s3") // Intentionally incomplete and unavailable.
	t.Setenv("IMVAULT_S3_BUCKET", "")
	output := filepath.Join(t.TempDir(), "restored")
	if err := runCommand([]string{"restore", "--input", backup, "--output", output}, nil, io.Discard); err != nil {
		t.Fatal("restore depended on source service configuration", err)
	}
}
