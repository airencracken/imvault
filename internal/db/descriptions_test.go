// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestDescriptionMigrationPreservesExistingFilesAndLimitsText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	database, err := sql.Open("sqlite", "file:"+path)
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
		if entry.Name() >= "022_file_descriptions.sql" {
			break
		}
		if err := applyMigration(t.Context(), database, entry.Name()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO files (id, original_name, ext, mime, size, sha256, created_at, visibility)
		VALUES ('old-photo', 'original.jpg', 'jpg', 'image/jpeg', 1, 'original-hash', 0, 'private')`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	var name, visibility, description string
	if err := database.QueryRow(`SELECT original_name, visibility, description FROM files WHERE id = 'old-photo'`).Scan(&name, &visibility, &description); err != nil {
		t.Fatal(err)
	}
	if name != "original.jpg" || visibility != "private" || description != "" {
		t.Fatal("upgrade changed existing file data")
	}
	for _, invalid := range []any{nil, strings.Repeat("x", 1001), "a\x00b"} {
		if _, err := database.Exec(`UPDATE files SET description = ? WHERE id = 'old-photo'`, invalid); err == nil {
			t.Fatal("schema accepted an invalid description")
		}
	}
	text := strings.Repeat("é", 1000)
	if _, err := database.Exec(`UPDATE files SET description = ? WHERE id = 'old-photo'`, text); err != nil {
		t.Fatal(err)
	}
	if err := migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT description FROM files WHERE id = 'old-photo'`).Scan(&description); err != nil || description != text {
		t.Fatal("rerunning migrations changed a saved description")
	}
}
