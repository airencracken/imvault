// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"imvault/internal/testutil"
)

const simpleSharingMigration = "035_simple_metadata_sharing.sql"

// migrateBefore applies every embedded migration that sorts before cutoff, so a
// test can seed rows in the shape an older release left them.
func migrateBefore(t *testing.T, ctx context.Context, database *sql.DB, cutoff string) {
	t.Helper()
	if _, err := database.ExecContext(ctx, schemaMigrationsDDL); err != nil {
		t.Fatal(err)
	}
	pending, err := Pending(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range pending {
		if name >= cutoff {
			break
		}
		if err := applyMigration(ctx, database, name); err != nil {
			t.Fatal(err)
		}
	}
}

// The old model, reproduced as an oracle. Three settings per file and per
// album, "inherit" deferring to visibility or to the EXIF choice, and the
// strictest opinion winning.
type legacyPolicy string

func parseLegacy(raw string) legacyPolicy {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "shown":
		return "shown"
	case "hidden":
		return "hidden"
	}
	return "inherit"
}

func strictest(policies ...legacyPolicy) legacyPolicy {
	out, decided := legacyPolicy("inherit"), false
	for _, p := range policies {
		if p == "inherit" {
			continue
		}
		if !decided || p == "hidden" {
			out, decided = p, true
		}
	}
	return out
}

func fallback(p, to legacyPolicy) legacyPolicy {
	if p == "inherit" {
		return to
	}
	return p
}

type legacyAlbum struct{ metadata, location string }

func legacyEffective(visibility, metadata, location string, albums []legacyAlbum) (camera, gps bool) {
	own := parseLegacy(metadata)
	if own == "inherit" {
		own = "shown"
		if strings.ToLower(strings.TrimSpace(visibility)) == "public" {
			own = "hidden"
		}
	}
	exif := []legacyPolicy{own}
	locations := []legacyPolicy{fallback(parseLegacy(location), own)}
	for _, a := range albums {
		m := parseLegacy(a.metadata)
		exif = append(exif, m)
		locations = append(locations, fallback(parseLegacy(a.location), m))
	}
	return strictest(exif...) == "shown", strictest(locations...) == "shown"
}

func TestSimpleSharingMigrationKeepsEffectiveVisibility(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "upgrade.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, database)
	migrateBefore(t, ctx, database, simpleSharingMigration)

	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	mustExec(`INSERT INTO users (id,username,password_hash,created_at) VALUES (1,'alice','hash',0)`)
	mustExec(`INSERT INTO blobs (sha256,size,object_key,created_at) VALUES ('hash',10,'o/hash',0)`)

	// Unchecked historical values the old parser read leniently.
	fileMetadata := []string{"inherit", "shown", "hidden", " SHOWN ", "Hidden ", "junk"}
	levels := []string{"inherit", "shown", "hidden"}
	visibilities := []string{"public", "members", "private"}

	albumIDs := map[legacyAlbum]int64{}
	var albumKinds []legacyAlbum
	for _, m := range levels {
		for _, l := range levels {
			kind := legacyAlbum{m, l}
			res, err := database.ExecContext(ctx, `INSERT INTO albums (user_id,title,slug,created_at,metadata,location) VALUES (1,?,?,0,?,?)`,
				m+l, m+"-"+l, m, l)
			if err != nil {
				t.Fatal(err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			albumIDs[kind] = id
			albumKinds = append(albumKinds, kind)
		}
	}

	type expectation struct{ camera, location bool }
	want := map[string]expectation{}
	random := rand.New(rand.NewSource(35))
	n := 0
	addFile := func(visibility, metadata, location string, albums []legacyAlbum) {
		n++
		id := fmt.Sprintf("f%04d", n)
		mustExec(`INSERT INTO files (id,user_id,original_name,ext,mime,size,sha256,created_at,metadata,location,visibility) VALUES (?,1,'p.jpg','.jpg','image/jpeg',10,'hash',0,?,?,?)`,
			id, metadata, location, visibility)
		for _, a := range albums {
			mustExec(`INSERT INTO album_files (album_id,file_id,added_at) VALUES (?,?,0)`, albumIDs[a], id)
		}
		camera, gps := legacyEffective(visibility, metadata, location, albums)
		want[id] = expectation{camera, gps}
	}
	for _, visibility := range visibilities {
		for _, metadata := range fileMetadata {
			for _, location := range levels {
				addFile(visibility, metadata, location, nil)
				for _, a := range albumKinds {
					addFile(visibility, metadata, location, []legacyAlbum{a})
				}
				for i := 0; i < 4; i++ {
					first, second := albumKinds[random.Intn(len(albumKinds))], albumKinds[random.Intn(len(albumKinds))]
					if first != second {
						addFile(visibility, metadata, location, []legacyAlbum{first, second})
					}
				}
			}
		}
	}

	if err := migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	// Running again is a no-op.
	if err := migrate(ctx, database); err != nil {
		t.Fatal(err)
	}

	rows, err := database.QueryContext(ctx, `SELECT id, show_camera, show_location FROM files`)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, rows)
	seen := 0
	for rows.Next() {
		var id string
		var camera, location bool
		if err := rows.Scan(&id, &camera, &location); err != nil {
			t.Fatal(err)
		}
		seen++
		expected := want[id]
		// Exactly the effective value, which also means nothing became
		// more visible than before.
		if camera != expected.camera || location != expected.location {
			t.Errorf("%s: got camera=%v location=%v, want %+v", id, camera, location, expected)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != len(want) || seen < 500 {
		t.Fatalf("checked %d of %d files", seen, len(want))
	}

	for _, column := range []string{"files.metadata", "files.location", "albums.metadata", "albums.location"} {
		table, name, _ := strings.Cut(column, ".")
		var count int
		if err := database.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, name).Scan(&count); err != nil || count != 0 {
			t.Errorf("%s survived the migration (%d, %v)", column, count, err)
		}
	}
	for _, invalid := range []any{2, -1, "shown", nil} {
		if _, err := database.ExecContext(ctx, `UPDATE files SET show_location = ? WHERE id = 'f0001'`, invalid); err == nil {
			t.Errorf("show_location accepted %v", invalid)
		}
		if _, err := database.ExecContext(ctx, `UPDATE files SET show_camera = ? WHERE id = 'f0001'`, invalid); err == nil {
			t.Errorf("show_camera accepted %v", invalid)
		}
	}
	// A row inserted without naming the columns hides location.
	mustExec(`INSERT INTO files (id,user_id,original_name,ext,mime,size,sha256,created_at,visibility) VALUES ('late',1,'p.jpg','.jpg','image/jpeg',10,'hash',0,'public')`)
	var location bool
	if err := database.QueryRowContext(ctx, `SELECT show_location FROM files WHERE id = 'late'`).Scan(&location); err != nil || location {
		t.Fatalf("default location = %v, %v", location, err)
	}
}

func TestSimpleSharingMigrationRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, database)
	migrateBefore(t, ctx, database, simpleSharingMigration)
	// An index on the old column makes DROP COLUMN fail half way through.
	if _, err := database.ExecContext(ctx, `CREATE INDEX block_drop ON albums(location)`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, database, simpleSharingMigration); err == nil {
		t.Fatal("migration succeeded despite the blocking index")
	}
	var count int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('files') WHERE name IN ('show_camera','show_location')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration left new columns behind: %d %v", count, err)
	}
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, simpleSharingMigration).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration was recorded: %d %v", count, err)
	}
}
