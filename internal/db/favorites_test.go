// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestFavoritesUpgradePreservesAccountsAndFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	if _, err := raw.Exec(schemaMigrationsDDL); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "021_favorites.sql" {
			break
		}
		body, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 0)`, entry.Name()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`
		INSERT INTO users (id, username, password_hash, role, created_at) VALUES (1, 'existing', 'existing-hash', 'admin', 0);
		INSERT INTO files (id, user_id, original_name, ext, mime, size, sha256, visibility, created_at)
		VALUES ('existing-photo', 1, 'original.png', 'png', 'image/png', 10, 'existing-hash', 'private', 0);`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	var role, hash, name string
	if err := database.QueryRow(`SELECT role, password_hash FROM users WHERE id = 1`).Scan(&role, &hash); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT original_name FROM files WHERE id = 'existing-photo'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if role != "admin" || hash != "existing-hash" || name != "original.png" {
		t.Fatal("migration changed existing data")
	}
	if _, err := database.Exec(`INSERT INTO favorites (user_id, file_id, created_at) VALUES (1, 'existing-photo', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var count int
	if err := reopened.QueryRow(`SELECT COUNT(*) FROM favorites WHERE user_id = 1 AND file_id = 'existing-photo'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("favorite did not survive reopening: count=%d err=%v", count, err)
	}
}
