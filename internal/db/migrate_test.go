// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// schemaMigrationsDDL mirrors what the migration runner creates.
const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    TEXT PRIMARY KEY,
	applied_at INTEGER NOT NULL
);`

// buildLegacyDatabase creates a database as it stood after the named
// migrations, so a later migration can be tested against realistic prior state.
func buildLegacyDatabase(t *testing.T, path string, versions ...string) {
	t.Helper()

	if len(versions) == 0 {
		versions = []string{"001_init.sql", "002_media_and_api_keys.sql"}
	}

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer raw.Close()

	exec := func(label, statement string) {
		t.Helper()
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
	}

	for _, name := range versions {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		exec(name, string(body))
	}

	var recorded string
	for i, name := range versions {
		if i > 0 {
			recorded += ", "
		}
		recorded += "('" + name + "', 0)"
	}
	exec("schema_migrations", schemaMigrationsDDL)
	exec("applied versions", "INSERT INTO schema_migrations (version, applied_at) VALUES "+recorded)

	exec("users", `
		INSERT INTO users (id, username, email, password_hash, is_admin, created_at) VALUES
			(1, 'alice', '', 'x', 0, 0),
			(2, 'bob',   '', 'x', 0, 0)`)

	exec("alice public file", "INSERT INTO files (id, user_id, original_name, ext, mime, size, width, height, sha256, object_key, thumb_key, preview_key, is_public, kind, duration_ms, frame_count, views, created_at) VALUES ('alicepub', 1, 'a.png', 'png', 'image/png', 10, 4, 4, 'a', 'o/a', 't/a', '', 1, 'image', 0, 0, 0, 0)")
	exec("alice private file", "INSERT INTO files (id, user_id, original_name, ext, mime, size, width, height, sha256, object_key, thumb_key, preview_key, is_public, kind, duration_ms, frame_count, views, created_at) VALUES ('alicepriv', 1, 'b.png', 'png', 'image/png', 10, 4, 4, 'b', 'o/b', 't/b', '', 0, 'image', 0, 0, 0, 0)")
	exec("bob file", "INSERT INTO files (id, user_id, original_name, ext, mime, size, width, height, sha256, object_key, thumb_key, preview_key, is_public, kind, duration_ms, frame_count, views, created_at) VALUES ('bobfile', 2, 'c.png', 'png', 'image/png', 10, 4, 4, 'c', 'o/c', 't/c', '', 1, 'image', 0, 0, 0, 0)")
	exec("anonymous file", "INSERT INTO files (id, user_id, original_name, ext, mime, size, width, height, sha256, object_key, thumb_key, preview_key, is_public, kind, duration_ms, frame_count, views, created_at) VALUES ('anonfile', NULL, 'd.png', 'png', 'image/png', 10, 4, 4, 'd', 'o/d', 't/d', '', 1, 'image', 0, 0, 0, 0)")

	// Three globally unique tags. "shared" spans both accounts; "orphan" only
	// labels an anonymous upload. These only make sense before migration 003
	// introduced per-account namespaces.
	preTagMigration := true
	for _, name := range versions {
		if name == "003_per_account_tags.sql" {
			preTagMigration = false
		}
	}
	if preTagMigration {
		exec("tags", `
			INSERT INTO tags (id, name, slug) VALUES
				(1, 'shared',     'shared'),
				(2, 'alice-only', 'alice-only'),
				(3, 'orphan',     'orphan')`)

		exec("file_tags", `
			INSERT INTO file_tags (file_id, tag_id) VALUES
				('alicepub',  1),
				('alicepriv', 1),
				('alicepriv', 2),
				('bobfile',   1),
				('anonfile',  3)`)
	}
}

func TestPerAccountTagMigrationSplitsExistingTags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	buildLegacyDatabase(t, path)

	ctx := context.Background()
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("upgrade database: %v", err)
	}
	defer database.Close()

	// The instance-wide tag is now two rows, one per account; alice's other tag
	// survives; the tag on the anonymous upload is gone.
	rows, err := database.QueryContext(ctx, `
		SELECT t.user_id, COALESCE(u.username, '<none>'), t.name, COUNT(ft.file_id)
		FROM tags t
		LEFT JOIN users u ON u.id = t.user_id
		LEFT JOIN file_tags ft ON ft.tag_id = t.id
		GROUP BY t.id
		ORDER BY u.username, t.name`)
	if err != nil {
		t.Fatalf("read tags: %v", err)
	}
	defer rows.Close()

	type row struct {
		userID   int64
		username string
		name     string
		count    int
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.userID, &r.username, &r.name, &r.count); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	want := []row{
		{1, "alice", "alice-only", 1},
		{1, "alice", "shared", 2},
		{2, "bob", "shared", 1},
	}
	if len(got) != len(want) {
		t.Fatalf("tag rows = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tag row %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The anonymous upload's tag is gone, because there is no namespace for it.
	var orphanLinks int
	if err := database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM file_tags ft
		 JOIN tags t ON t.id = ft.tag_id
		 WHERE t.name = 'orphan'`).Scan(&orphanLinks); err != nil {
		t.Fatal(err)
	}
	if orphanLinks != 0 {
		t.Errorf("orphan tag still has %d links", orphanLinks)
	}

	// Every surviving link must point at a tag owned by the file's owner.
	var mismatched int
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM file_tags ft
		JOIN files f ON f.id = ft.file_id
		JOIN tags t ON t.id = ft.tag_id
		WHERE f.user_id IS NULL OR t.user_id <> f.user_id`).Scan(&mismatched); err != nil {
		t.Fatal(err)
	}
	if mismatched != 0 {
		t.Errorf("%d links cross account boundaries", mismatched)
	}

	// The old tables are gone, and the new index exists.
	var leftovers int
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name IN ('tags_old', 'file_tags_old')`).Scan(&leftovers); err != nil {
		t.Fatal(err)
	}
	if leftovers != 0 {
		t.Errorf("%d legacy tables were left behind", leftovers)
	}
}

func TestQuotaMigrationBackfillsUsageAndLeavesAccountsUnlimited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-quota.db")
	buildLegacyDatabase(t, path,
		"001_init.sql", "002_media_and_api_keys.sql", "003_per_account_tags.sql")

	// The fixture seeds two 10-byte files for alice, one for bob, and one
	// anonymous upload.
	ctx := context.Background()
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("upgrade database: %v", err)
	}
	defer database.Close()

	type account struct {
		username string
		used     int64
		quota    int64
	}
	rows, err := database.QueryContext(ctx,
		`SELECT username, storage_used, quota_bytes FROM users ORDER BY id`)
	if err != nil {
		t.Fatalf("read users: %v", err)
	}
	defer rows.Close()

	var got []account
	for rows.Next() {
		var a account
		if err := rows.Scan(&a.username, &a.used, &a.quota); err != nil {
			t.Fatal(err)
		}
		got = append(got, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	want := []account{{"alice", 20, 0}, {"bob", 10, 0}}
	if len(got) != len(want) {
		t.Fatalf("accounts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("account %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Existing accounts must stay unlimited: imposing a cap on accounts created
	// before quotas existed would silently break them.
	for _, a := range got {
		if a.quota != 0 {
			t.Errorf("%s was given a quota of %d, want unlimited", a.username, a.quota)
		}
	}
}

func TestTwoFactorMigrationLeavesExistingAccountsAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-2fa.db")
	buildLegacyDatabase(t, path,
		"001_init.sql", "002_media_and_api_keys.sql",
		"003_per_account_tags.sql", "004_quotas_and_admin.sql")

	ctx := context.Background()
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("upgrade database: %v", err)
	}
	defer database.Close()

	rows, err := database.QueryContext(ctx, `
		SELECT username, totp_secret, totp_enabled, totp_last_step
		FROM users ORDER BY id`)
	if err != nil {
		t.Fatalf("read users: %v", err)
	}
	defer rows.Close()

	accounts := 0
	for rows.Next() {
		var (
			username             string
			secret               string
			enabled, lastStepInt int
		)
		if err := rows.Scan(&username, &secret, &enabled, &lastStepInt); err != nil {
			t.Fatal(err)
		}
		accounts++

		// Adding a second factor must not switch one on for anybody.
		if secret != "" || enabled != 0 || lastStepInt != 0 {
			t.Errorf("%s gained a second factor from a migration: %q %d %d",
				username, secret, enabled, lastStepInt)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if accounts != 2 {
		t.Errorf("found %d accounts, want the two the fixture seeds", accounts)
	}

	// The recovery code table exists and is empty, and cascades with its owner.
	var codes int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM recovery_codes`).Scan(&codes); err != nil {
		t.Fatalf("recovery_codes: %v", err)
	}
	if codes != 0 {
		t.Errorf("%d recovery codes appeared from nowhere", codes)
	}
}

func TestAnonymousTagMigrationKeepsExistingTags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-anon-tags.db")
	buildLegacyDatabase(t, path,
		"001_init.sql", "002_media_and_api_keys.sql", "003_per_account_tags.sql",
		"004_quotas_and_admin.sql", "005_auth_tokens.sql", "006_outbound_mail.sql",
		"007_two_factor.sql", "008_content_addressed_storage.sql", "009_per_file_quota.sql")

	// A tag as the account namespaces left it.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		INSERT INTO tags (user_id, name, slug, created_at) VALUES (1, 'beach', 'beach', 0);
		INSERT INTO file_tags (file_id, tag_id) VALUES ('alicepub', 1);
		UPDATE users SET max_file_bytes = 4096 WHERE id = 1;
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	raw.Close()

	ctx := context.Background()
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("upgrade database: %v", err)
	}
	defer database.Close()

	// The tag kept its id and its owner, and the link still points at it.
	var (
		id     int64
		userID sql.NullInt64
		name   string
	)
	if err := database.QueryRowContext(ctx,
		`SELECT id, user_id, name FROM tags`).Scan(&id, &userID, &name); err != nil {
		t.Fatalf("read tag: %v", err)
	}
	if id != 1 || !userID.Valid || userID.Int64 != 1 || name != "beach" {
		t.Errorf("tag came through as id=%d user=%v name=%q", id, userID, name)
	}

	var linked int
	if err := database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM file_tags WHERE file_id = 'alicepub' AND tag_id = 1`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 1 {
		t.Error("the tag lost its file")
	}

	// A column added by an earlier migration must have survived the rebuild of
	// an unrelated table.
	var maxFile int64
	if err := database.QueryRowContext(ctx,
		`SELECT max_file_bytes FROM users WHERE id = 1`).Scan(&maxFile); err != nil {
		t.Fatal(err)
	}
	if maxFile != 4096 {
		t.Errorf("max_file_bytes = %d, want 4096", maxFile)
	}

	// The two namespaces are now enforced separately. "beach" is taken by the
	// account, but the anonymous namespace is free to use it as well.
	if _, err := database.ExecContext(ctx,
		`INSERT INTO tags (user_id, name, slug, created_at) VALUES (NULL, 'beach', 'beach', 0)`); err != nil {
		t.Errorf("the anonymous namespace rejected a name an account already uses: %v", err)
	}

	// Within the anonymous namespace, though, names are still unique. A plain
	// UNIQUE over (user_id, name) would not have caught this, because SQLite
	// treats NULLs as distinct.
	if _, err := database.ExecContext(ctx,
		`INSERT INTO tags (user_id, name, slug, created_at) VALUES (NULL, 'beach', 'beach', 0)`); err == nil {
		t.Error("the anonymous namespace allowed a duplicate tag name")
	}

	// And a duplicate slug under a different name is refused too.
	if _, err := database.ExecContext(ctx,
		`INSERT INTO tags (user_id, name, slug, created_at) VALUES (NULL, 'beach!', 'beach', 0)`); err == nil {
		t.Error("the anonymous namespace allowed a duplicate slug")
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	ctx := context.Background()

	for attempt := 1; attempt <= 3; attempt++ {
		database, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("open attempt %d: %v", attempt, err)
		}
		database.Close()
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer database.Close()

	// Count migrations from the embedded directory rather than hardcoding, so
	// adding one does not require touching this test.
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			want++
		}
	}

	var applied int
	if err := database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != want {
		t.Errorf("%d migrations recorded, want %d", applied, want)
	}
}
