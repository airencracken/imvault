// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/airencracken/comfylib/analytics"

	"imvault/internal/testutil"
)

func TestForgetAnalyticsDeletesIdentifyingRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	migrateBefore(t, ctx, database, forgetAnalyticsMigration)
	now := time.Now()
	if err := analytics.Initialize(ctx, database, now); err != nil {
		t.Fatal(err)
	}
	// Ordinary rows, and odd identifiers a stored row could still hold.
	if _, err := database.ExecContext(ctx, `INSERT INTO comfy_analytics_activity(day,kind,member,item,count,last_at) VALUES(?, 'login', ?, '', 1, ?)`,
		now.Unix()/86400, "'; DROP TABLE users; --", now.Unix()); err != nil {
		t.Fatal(err)
	}
	for i, member := range []string{"1", "42", "~", "<script>"} {
		if err := analytics.RecordLogin(ctx, database, member, now); err != nil {
			t.Fatal(err)
		}
		if err := analytics.RecordView(ctx, database, analytics.Image, member, "file"+member, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	countRows := func() int {
		var n int
		if err := database.QueryRowContext(ctx, `SELECT count(*) FROM comfy_analytics_activity`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if countRows() != 9 {
		t.Fatalf("seeded %d rows, want 9", countRows())
	}
	testutil.Close(t, database)

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, reopened)
	database = reopened
	if n := countRows(); n != 0 {
		t.Fatalf("%d identifying analytics rows survived the upgrade", n)
	}
	var users int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'users'`).Scan(&users); err != nil || users != 1 {
		t.Fatal("the upgrade damaged application tables")
	}
}

func TestForgetAnalyticsOnAFreshDatabase(t *testing.T) {
	ctx := context.Background()
	// A new database reaches this migration before Comfylib creates its
	// tables; the migration must not fail there.
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
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, database)
	migrateBefore(t, ctx, database, forgetAnalyticsMigration)
	if err := analytics.Initialize(ctx, database, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := analytics.RecordLogin(ctx, database, "7", time.Now()); err != nil {
		t.Fatal(err)
	}
	// A trigger that refuses the delete stands in for a failed write.
	if _, err := database.ExecContext(ctx, `CREATE TRIGGER refuse BEFORE DELETE ON comfy_analytics_activity BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, database, forgetAnalyticsMigration); err == nil {
		t.Fatal("a failed delete was not reported")
	}
	var recorded int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, forgetAnalyticsMigration).Scan(&recorded); err != nil || recorded != 0 {
		t.Fatal("a failed migration was recorded as applied")
	}
}
