// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"imvault/internal/testutil"
)

// futureMigration stands in for a migration only a newer Imvault ships.
const futureMigration = "999_from_a_newer_imvault.sql"

// currentDatabase creates a database at this binary's schema.
func currentDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "imvault.db")
	database, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, database)
	return path
}

// recordMigrations marks migrations as applied, as another binary would have.
func recordMigrations(t *testing.T, path string, names ...string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, raw)
	if _, err := raw.Exec(schemaMigrationsDDL); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 0)`, name); err != nil {
			t.Fatal(err)
		}
	}
}

// appliedNames reads schema_migrations directly, sorted.
func appliedNames(t *testing.T, path string) []string {
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

func TestOpenRefusesADatabaseFromANewerImvault(t *testing.T) {
	path := currentDatabase(t)
	recordMigrations(t, path, futureMigration)

	database, err := Open(t.Context(), path)
	if err == nil {
		testutil.Close(t, database)
		t.Fatal("an older binary opened a database a newer Imvault had migrated")
	}
	var newer *NewerSchemaError
	if !errors.As(err, &newer) {
		t.Fatalf("error is %T, want *NewerSchemaError: %v", err, err)
	}
	if !slices.Equal(newer.Unknown, []string{futureMigration}) || newer.Path != path {
		t.Fatalf("error names %v at %q", newer.Unknown, newer.Path)
	}
	// The operator reads this in the journal, so it has to say what happened
	// and what to do about it.
	for _, want := range []string{
		"database " + path,
		"upgraded by a newer Imvault",
		futureMigration,
		"older Imvault binary running against a newer database",
		"Install the newer Imvault again",
		"restore the backup taken before the upgrade",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q:\n%s", want, err)
		}
	}
}

// An older binary must not apply even its own migrations to a newer schema:
// the refusal comes before anything is written.
func TestANewerDatabaseIsNotMigratedFurther(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.db")
	buildLegacyDatabase(t, path)
	recordMigrations(t, path, futureMigration)
	before := appliedNames(t, path)

	if database, err := Open(t.Context(), path); err == nil {
		testutil.Close(t, database)
		t.Fatal("opened a newer database")
	}
	if after := appliedNames(t, path); !slices.Equal(before, after) {
		t.Fatalf("refused open still changed schema_migrations:\nbefore %v\nafter  %v", before, after)
	}
}

// Migration names are whatever another binary wrote, so they are reported
// verbatim, never interpreted, and never break the refusal.
func TestUnknownMigrationNamesAreReportedVerbatim(t *testing.T) {
	for _, name := range []string{
		"'; DROP TABLE users; --",
		"",
		"../../etc/passwd",
		"000_sorts_first.sql",
		"029_next.sql\nIMVAULT_FAKE=1",
		strings.Repeat("x", 4096),
		"\u202e" + "lqs.evil",
	} {
		path := currentDatabase(t)
		recordMigrations(t, path, name)
		database, err := Open(t.Context(), path)
		if err == nil {
			testutil.Close(t, database)
			t.Fatalf("opened a database with unknown migration %q", name)
		}
		var newer *NewerSchemaError
		if !errors.As(err, &newer) || !slices.Equal(newer.Unknown, []string{name}) {
			t.Fatalf("unknown migration %q: %v", name, err)
		}
		if got := appliedNames(t, path); !slices.Contains(got, "001_init.sql") || len(got) != len(mustEmbedded(t))+1 {
			t.Fatalf("refusal changed the database: %v", got)
		}
	}
}

func mustEmbedded(t *testing.T) []string {
	t.Helper()
	names, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// For any applied subset of the embedded migrations, plus any set of unknown
// ones, the runner either lists exactly the rest in order or names exactly the
// unknown ones.
func TestPendingMigrationsProperty(t *testing.T) {
	known := mustEmbedded(t)
	rng := rand.New(rand.NewPCG(1, 2))
	for range 500 {
		applied := map[string]bool{}
		var want []string
		for _, name := range known {
			if rng.IntN(2) == 0 {
				applied[name] = true
			} else {
				want = append(want, name)
			}
		}
		var unknown []string
		for i := range rng.IntN(3) {
			name := string(rune('a'+i)) + "_" + strings.Repeat("z", rng.IntN(5)) + ".sql"
			applied[name] = true
			unknown = append(unknown, name)
		}
		sort.Strings(unknown)

		pending, err := pendingAgainst(applied)
		if len(unknown) == 0 {
			if err != nil || !slices.Equal(pending, want) {
				t.Fatalf("applied %v: pending %v (%v), want %v", applied, pending, err, want)
			}
			continue
		}
		var newer *NewerSchemaError
		if !errors.As(err, &newer) || pending != nil || !slices.Equal(newer.Unknown, unknown) {
			t.Fatalf("applied %v: pending %v, error %v, want unknown %v", applied, pending, err, unknown)
		}
	}
}

func TestOpenCurrentLeavesTheSchemaAlone(t *testing.T) {
	t.Run("current", func(t *testing.T) {
		database, err := OpenCurrent(t.Context(), currentDatabase(t))
		if err != nil {
			t.Fatal(err)
		}
		testutil.Close(t, database)
	})
	t.Run("older", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "older.db")
		buildLegacyDatabase(t, path)
		before := appliedNames(t, path)
		database, err := OpenCurrent(t.Context(), path)
		if err == nil {
			testutil.Close(t, database)
			t.Fatal("opened a database with pending migrations")
		}
		var older *OlderSchemaError
		if !errors.As(err, &older) || older.Path != path || len(older.Pending) != len(mustEmbedded(t))-len(before) {
			t.Fatalf("error = %v", err)
		}
		for _, want := range []string{"predates this Imvault binary", "does not migrate", "003_per_account_tags.sql"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error does not say %q: %s", want, err)
			}
		}
		if after := appliedNames(t, path); !slices.Equal(before, after) {
			t.Fatalf("OpenCurrent migrated: %v -> %v", before, after)
		}
	})
	t.Run("newer", func(t *testing.T) {
		path := currentDatabase(t)
		recordMigrations(t, path, futureMigration)
		database, err := OpenCurrent(t.Context(), path)
		var newer *NewerSchemaError
		if err == nil {
			testutil.Close(t, database)
		}
		if !errors.As(err, &newer) {
			t.Fatalf("error = %v", err)
		}
	})
}

// Pending only reads, so restore can ask it about an immutable snapshot.
func TestPendingReadsAnImmutableSnapshot(t *testing.T) {
	ctx := context.Background()
	empty := filepath.Join(t.TempDir(), "empty.db")
	recordMigrations(t, empty)
	newer := currentDatabase(t)
	recordMigrations(t, newer, futureMigration)
	bare := filepath.Join(t.TempDir(), "bare.db")
	raw, err := sql.Open("sqlite", "file:"+bare)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE unrelated (x)`); err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, raw)

	for _, tc := range []struct {
		path    string
		pending int
		newer   bool
	}{
		{currentDatabase(t), 0, false},
		{empty, len(mustEmbedded(t)), false},
		{bare, len(mustEmbedded(t)), false},
		{newer, 0, true},
	} {
		u := &url.URL{Scheme: "file", Path: tc.path, RawQuery: "mode=ro&immutable=1"}
		snapshot, err := sql.Open("sqlite", u.String())
		if err != nil {
			t.Fatal(err)
		}
		pending, err := Pending(ctx, snapshot)
		testutil.Close(t, snapshot)
		var newerErr *NewerSchemaError
		if errors.As(err, &newerErr) != tc.newer || (!tc.newer && err != nil) || len(pending) != tc.pending {
			t.Errorf("%s: pending %d, error %v", filepath.Base(tc.path), len(pending), err)
		}
		if newerErr != nil && !strings.HasPrefix(err.Error(), "the database was upgraded") {
			t.Errorf("a pathless error does not read naturally: %s", err)
		}
	}
}
