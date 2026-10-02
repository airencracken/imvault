// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"database/sql"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"imvault/internal/db"
	"imvault/internal/secrets"
	"imvault/internal/testutil"
)

// existingInstance creates a database and key at this binary's schema, then
// runs change against the database directly.
func existingInstance(t *testing.T, change string, args ...any) string {
	t.Helper()
	path := adminTestEnvironment(t)
	database, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if change != "" {
		if _, err := database.Exec(change, args...); err != nil {
			t.Fatal(err)
		}
	}
	testutil.Close(t, database)
	if _, err := secrets.Load(filepath.Join(filepath.Dir(path), "secret.key"), ""); err != nil {
		t.Fatal(err)
	}
	return path
}

func migrationRows(t *testing.T, path string) []string {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, raw)
	rows, err := raw.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, rows)
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

// Every command that opens the database, the server first among them, refuses
// one a newer Imvault has migrated, and says why.
func TestEveryDatabaseCommandRefusesANewerDatabase(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{"server", nil, ""},
		{"backup", []string{"backup", "--output", ""}, ""},
		{"migrate-storage", []string{"migrate-storage"}, ""},
		{"rebuild-thumbnails", []string{"rebuild-thumbnails"}, ""},
		{"refresh-metadata", []string{"refresh-metadata"}, ""},
		{"create-admin", []string{"create-admin", "--username", "late_admin", "--password-stdin"}, "a-long-enough-password\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := existingInstance(t, `INSERT INTO schema_migrations (version, applied_at) VALUES ('999_from_a_newer_imvault.sql', 0)`)
			// The address is taken, so a server that wrongly got past the
			// database fails to listen instead of serving until the timeout.
			busy, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer testutil.Close(t, busy)
			t.Setenv("IMVAULT_ADDR", busy.Addr().String())
			t.Setenv("IMVAULT_DEST_STORAGE", "disk")
			t.Setenv("IMVAULT_DEST_DATA_DIR", t.TempDir())
			args := slices.Clone(tc.args)
			output := filepath.Join(t.TempDir(), "backup")
			if tc.name == "backup" {
				args[2] = output
			}
			before := migrationRows(t, path)
			err = runCommand(args, strings.NewReader(tc.stdin), io.Discard)
			if err == nil {
				t.Fatal("ran against a newer database")
			}
			for _, want := range []string{"upgraded by a newer Imvault", "999_from_a_newer_imvault.sql", "older Imvault binary running against a newer database"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error does not say %q: %v", want, err)
				}
			}
			if after := migrationRows(t, path); !slices.Equal(before, after) {
				t.Fatalf("refused command changed schema_migrations: %v", after)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("refused backup was published")
			}
		})
	}
}

// A backup is the copy to fall back on if an upgrade goes wrong, so it must
// not apply the upgrade's migrations first. A newer binary refuses a database
// it would migrate and leaves it exactly as it was. Migration 027 is a plain
// UPDATE, so marking it unapplied gives a database that db.Open would migrate
// successfully.
func TestBackupDoesNotMigrateAnOlderDatabase(t *testing.T) {
	path := existingInstance(t, `DELETE FROM schema_migrations WHERE version = '027_revoke_orphaned_invites.sql'`)
	before := migrationRows(t, path)
	output := filepath.Join(t.TempDir(), "backup")
	err := runCommand([]string{"backup", "--output", output}, nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "predates this Imvault binary") || !strings.Contains(err.Error(), "027_revoke_orphaned_invites.sql") {
		t.Fatalf("backup of an older database: %v", err)
	}
	if after := migrationRows(t, path); !slices.Equal(before, after) {
		t.Fatalf("backup migrated the database: %v -> %v", before, after)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("refused backup was published")
	}
}
