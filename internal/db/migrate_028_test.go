// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

// buildDatabaseBefore applies every migration that sorts before the named one
// and records them, the way an instance one release older would stand.
func buildDatabaseBefore(t *testing.T, path, before string) *sql.DB {
	t.Helper()

	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)

	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(schemaMigrationsDDL); err != nil {
		t.Fatal(err)
	}
	for _, full := range names {
		name := filepath.Base(full)
		if name >= before {
			break
		}
		body, err := migrationsFS.ReadFile(full)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 0)`, name); err != nil {
			t.Fatal(err)
		}
	}
	return raw
}

func TestPasswordStateMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-028.db")
	raw := buildDatabaseBefore(t, path, "028_password_state_and_album_reports.sql")
	for _, statement := range []string{
		`INSERT INTO users (id, username, email, password_hash, created_at) VALUES
			(1, 'local', '', 'x', 100),
			(2, 'viaprovider', '', 'x', 100),
			(3, 'linkedlater', '', 'x', 100)`,
		`INSERT INTO user_identities (user_id, issuer, subject, email, created_at) VALUES
			(2, 'https://id.example', 'made-here', '', 101),
			(3, 'https://id.example', 'linked', '', 5000)`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()

	passwordSet := func(id int) int {
		t.Helper()
		var set int
		if err := database.QueryRowContext(ctx, `SELECT password_set FROM users WHERE id = ?`, id).Scan(&set); err != nil {
			t.Fatal(err)
		}
		return set
	}

	// The backfill: only the account a provider plainly made loses its password.
	for id, want := range map[int]int{1: 1, 2: 0, 3: 1} {
		if got := passwordSet(id); got != want {
			t.Errorf("account %d password_set = %d, want %d", id, got, want)
		}
	}

	// The schema: a new account defaults to having a password, and the column
	// holds a boolean and nothing else.
	if _, err := database.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, created_at) VALUES (4, 'fresh', '', 'x', 0)`); err != nil {
		t.Fatal(err)
	}
	if got := passwordSet(4); got != 1 {
		t.Errorf("a new account password_set = %d, want 1", got)
	}
	if _, err := database.ExecContext(ctx, `UPDATE users SET password_set = 2 WHERE id = 4`); err == nil {
		t.Error("password_set accepted a value that is not a boolean")
	}
	if _, err := database.ExecContext(ctx, `UPDATE users SET password_set = NULL WHERE id = 4`); err == nil {
		t.Error("password_set accepted NULL")
	}

	// The trigger: writing a new hash records that a password now exists, and
	// nothing else does.
	if _, err := database.ExecContext(ctx, `UPDATE users SET email = 'a@example.com' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if got := passwordSet(2); got != 0 {
		t.Error("an unrelated update set the password flag")
	}
	if _, err := database.ExecContext(ctx, `UPDATE users SET password_hash = 'chosen' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if got := passwordSet(2); got != 1 {
		t.Error("choosing a password did not record it")
	}
}

func TestAlbumReportMigrationUsesIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-028-reports.db")
	raw := buildDatabaseBefore(t, path, "028_password_state_and_album_reports.sql")
	for _, statement := range []string{
		`INSERT INTO users (id, username, email, password_hash, created_at) VALUES (1, 'alice', '', 'x', 0)`,
		`INSERT INTO albums (id, user_id, title, slug, created_at) VALUES
			(7, 1, 'Holiday', 'holiday', 0),
			(8, 1, '2024', '2024', 0)`,
		`INSERT INTO reports (id, target_kind, target_id, reporter, reason, created_at) VALUES
			(1, 'album', 'holiday', 'bob', 'spam', 0),
			(2, 'album', '2024', 'bob', 'spam', 0),
			(3, 'album', 'deleted-one', 'bob', 'spam', 0),
			(4, 'album', '3', 'bob', 'spam', 0),
			(5, 'file', 'abc123', 'bob', 'spam', 0)`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()

	for id, want := range map[int]string{
		1: "7",                // a live album becomes its id
		2: "8",                // a numeric slug is still a slug
		3: "gone:deleted-one", // an album already gone can never resolve
		4: "gone:3",           // a numeric slug of a gone album must not become an id
		5: "abc123",           // file reports are untouched
	} {
		var got string
		if err := database.QueryRowContext(ctx, `SELECT target_id FROM reports WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("report %d target = %q, want %q", id, got, want)
		}
	}
}
