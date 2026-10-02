// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	"imvault/internal/testutil"
)

func TestLocationMigrationPreservesExistingSharing(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "upgrade.db"))
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
		if entry.Name() >= "026_location_policy.sql" {
			break
		}
		if err := applyMigration(t.Context(), database, entry.Name()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO users (id,username,password_hash,created_at) VALUES (1,'alice','hash',0)`); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{"inherit", "shown", "hidden"} {
		if _, err := database.Exec(`INSERT INTO files (id,user_id,original_name,ext,mime,size,sha256,created_at,metadata,visibility) VALUES (?,1,'photo.jpg','.jpg','image/jpeg',10,'hash',0,?,'public')`, policy, policy); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO albums (user_id,title,slug,created_at,metadata) VALUES (1,?,?,0,?)`, policy, policy, policy); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := migrate(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"files", "albums"} {
			var count int
			if err := database.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE location = 'inherit' AND metadata IN ('inherit','shown','hidden')`).Scan(&count); err != nil || count != 3 {
				t.Fatalf("%s migration: %d %v", table, count, err)
			}
			for _, invalid := range []any{"", "public", nil} {
				if _, err := database.Exec(`UPDATE `+table+` SET location = ?`, invalid); err == nil {
					t.Fatalf("%s accepts invalid location %v", table, invalid)
				}
			}
		}
	}
}
