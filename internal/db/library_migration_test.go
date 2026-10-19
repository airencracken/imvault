// SPDX-License-Identifier: AGPL-3.0-or-later
package db

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestCaptureMigrationBackfillsExistingMetadataInBatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw := buildDatabaseBefore(t, path, "033_trash.sql")
	for i := 0; i < 405; i++ {
		details := `{"taken":"29 February 2024 at 23:59","camera":"Example"}`
		if i%3 == 0 {
			details = `{"taken":"invalid"}`
		}
		if i%3 == 1 {
			details = `not JSON`
		}
		if _, err := raw.Exec(`INSERT INTO blobs(sha256,size,object_key,thumb_key,preview_key,details_json,created_at) VALUES(?,1,'original','thumb','preview',?,100)`, fmt.Sprintf("%04d", i), details); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var dated, unknown int
	if err := db.QueryRow(`SELECT COUNT(*) FROM blobs WHERE taken_at='2024-02-29T23:59:00'`).Scan(&dated); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM blobs WHERE taken_at=''`).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if dated != 135 || unknown != 270 {
		t.Fatal("backfill counts", dated, unknown)
	}
	var preserved string
	if err := db.QueryRow(`SELECT details_json FROM blobs WHERE sha256='0002'`).Scan(&preserved); err != nil || preserved != `{"taken":"29 February 2024 at 23:59","camera":"Example"}` {
		t.Fatal("migration changed cached metadata", preserved, err)
	}
	for _, index := range []string{"files_trash", "blobs_taken"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&n); err != nil || n != 1 {
			t.Fatal("missing index", index, n, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,created_at) VALUES(1,'owner','hash',100)`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO files(id,user_id,original_name,ext,mime,size,width,height,sha256,created_at,trashed_at) VALUES('anon',NULL,'photo','jpg','image/jpeg',1,1,1,'0002',100,101)`,
		`INSERT INTO files(id,user_id,original_name,ext,mime,size,width,height,sha256,created_at,trashed_at) VALUES('negative',1,'photo','jpg','image/jpeg',1,1,1,'0002',100,-1)`,
		`INSERT INTO files(id,user_id,original_name,ext,mime,size,width,height,sha256,created_at,trashed_at) VALUES('text',1,'photo','jpg','image/jpeg',1,1,1,'0002',100,'invalid')`,
		`INSERT INTO files(id,user_id,original_name,ext,mime,size,width,height,sha256,created_at,trashed_at) VALUES('fraction',1,'photo','jpg','image/jpeg',1,1,1,'0002',100,1.5)`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatal("invalid trash state accepted", statement)
		}
	}
}

func TestCaptureMigrationRollsBackSchemaAndBackfillOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw := buildDatabaseBefore(t, path, "034_capture_dates.sql")
	defer raw.Close()
	for _, statement := range []string{
		`INSERT INTO blobs(sha256,size,object_key,thumb_key,preview_key,details_json,created_at) VALUES('a',1,'a','at','ap','{"taken":"1 January 2024 at 12:00"}',100),('b',1,'b','bt','bp','{"taken":"2 January 2024 at 12:00"}',100)`,
		`CREATE TRIGGER fail_dates BEFORE UPDATE ON blobs WHEN OLD.sha256='b' BEGIN SELECT RAISE(ABORT,'injected migration failure'); END`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := applyMigration(t.Context(), raw, "034_capture_dates.sql"); err == nil {
		t.Fatal("injected migration failure accepted")
	}
	var recorded, columns int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version='034_capture_dates.sql'`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('blobs') WHERE name='taken_at'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 || columns != 0 {
		t.Fatal("partial migration persisted", recorded, columns)
	}
	if _, err := raw.Exec(`DROP TRIGGER fail_dates`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(t.Context(), raw, "034_capture_dates.sql"); err != nil {
		t.Fatal("retry migration", err)
	}
}
