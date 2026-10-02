// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	"imvault/internal/testutil"
)

func TestOrphanedInviteMigrationRevokesOnlyIssuerlessOpenCodes(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "upgrade.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, database)
	if _, err := database.Exec(schemaMigrationsDDL); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "027_revoke_orphaned_invites.sql" {
			break
		}
		if err := applyMigration(t.Context(), database, entry.Name()); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, created_at) VALUES (1, 'issuer', 'x', 0)`,
		`INSERT INTO invites (id, prefix, code_hash, created_by, created_at) VALUES (1, 'kept', 'h', 1, 0)`,
		`INSERT INTO invites (id, prefix, code_hash, created_by, created_at) VALUES (2, 'orphan', 'h', NULL, 0)`,
		`INSERT INTO invites (id, prefix, code_hash, created_by, created_at, revoked_at) VALUES (3, 'already', 'h', NULL, 0, 42)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	// Twice: the second run must find nothing to apply and change nothing.
	for i := 0; i < 2; i++ {
		if err := migrate(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		want := map[int]func(sql.NullInt64) bool{
			1: func(v sql.NullInt64) bool { return !v.Valid },
			2: func(v sql.NullInt64) bool { return v.Valid && v.Int64 > 42 },
			3: func(v sql.NullInt64) bool { return v.Valid && v.Int64 == 42 },
		}
		for id, ok := range want {
			var revoked sql.NullInt64
			if err := database.QueryRow(`SELECT revoked_at FROM invites WHERE id = ?`, id).Scan(&revoked); err != nil || !ok(revoked) {
				t.Fatalf("pass %d, invite %d: revoked_at %+v (%v)", i, id, revoked, err)
			}
		}
	}
	var applied int
	if err := database.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = '027_revoke_orphaned_invites.sql'`).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("migration recorded %d times (%v)", applied, err)
	}
}
