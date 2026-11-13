// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"imvault/internal/testutil"
)

// legacyActivity is the per-account table Comfylib 0.6.0 created.
const legacyActivity = `CREATE TABLE comfy_analytics_activity (
 day INTEGER NOT NULL, kind TEXT NOT NULL, member TEXT NOT NULL, item TEXT NOT NULL,
 count INTEGER NOT NULL CHECK(count>0), last_at INTEGER NOT NULL, PRIMARY KEY(day,kind,member,item))`

func tableExists(t *testing.T, database *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestForgetAnalyticsDeletesIdentifyingRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	migrateBefore(t, ctx, database, forgetAnalyticsMigration)
	if _, err := database.ExecContext(ctx, legacyActivity); err != nil {
		t.Fatal(err)
	}
	// Ordinary rows, and odd identifiers a stored row could still hold.
	for _, member := range []string{"1", "42", "~", "'; DROP TABLE users; --", "<script>"} {
		if _, err := database.ExecContext(ctx, `INSERT INTO comfy_analytics_activity VALUES (1,'login',?,'',1,1), (1,'image',?,'file',1,1)`, member, member); err != nil {
			t.Fatal(err)
		}
	}
	// The migration alone removes it, without relying on Comfylib's start-up.
	if err := migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	if tableExists(t, database, "comfy_analytics_activity") {
		t.Fatal("the migration kept the per-account analytics table")
	}
	testutil.Close(t, database)

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, reopened)
	if !tableExists(t, reopened, "users") || !tableExists(t, reopened, "comfy_analytics_routes") {
		t.Fatal("the upgrade damaged application or site health tables")
	}
}

func TestForgetAnalyticsOnAFreshDatabase(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, database)
	var applied int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, forgetAnalyticsMigration).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("migration not recorded: %d %v", applied, err)
	}
}

func TestForgetAnalyticsRollsBackWithTheMigration(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "rollback.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, database)
	migrateBefore(t, ctx, database, forgetAnalyticsMigration)
	// A row elsewhere that refers to the table makes dropping it fail, standing
	// in for a failed write.
	for _, statement := range []string{
		legacyActivity,
		`INSERT INTO comfy_analytics_activity VALUES (1,'login','7','',1,1)`,
		`CREATE TABLE pin (day INTEGER, kind TEXT, member TEXT, item TEXT,
			FOREIGN KEY(day,kind,member,item) REFERENCES comfy_analytics_activity(day,kind,member,item))`,
		`INSERT INTO pin VALUES (1,'login','7','')`,
	} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := applyMigration(ctx, database, forgetAnalyticsMigration); err == nil {
		t.Fatal("a failed drop was not reported")
	}
	var recorded int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, forgetAnalyticsMigration).Scan(&recorded); err != nil || recorded != 0 {
		t.Fatal("a failed migration was recorded as applied")
	}
	if !tableExists(t, database, "comfy_analytics_activity") {
		t.Fatal("a failed migration still dropped the table")
	}
}
