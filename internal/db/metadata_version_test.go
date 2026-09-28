// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMetadataVersionMigrationPreservesDetails(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(schemaMigrationsDDL); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "025_metadata_version.sql" {
			break
		}
		if err := applyMigration(t.Context(), database, entry.Name()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO blobs (sha256, size, object_key, thumb_key, preview_key, refcount, created_at, details_json) VALUES ('hash', 10, 'original.jpg', '', '', 1, 0, '{"camera":"Old Camera"}')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrate(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		var version int
		var details, key string
		if err := database.QueryRow(`SELECT details_version, details_json, object_key FROM blobs WHERE sha256 = 'hash'`).Scan(&version, &details, &key); err != nil || version != 0 || details != `{"camera":"Old Camera"}` || key != "original.jpg" {
			t.Fatalf("migration changed original details: %d %s %s (%v)", version, details, key, err)
		}
	}
	for _, invalid := range []any{-1, nil} {
		if _, err := database.Exec(`UPDATE blobs SET details_version = ?`, invalid); err == nil {
			t.Fatal("metadata version schema accepted invalid value")
		}
	}
}
