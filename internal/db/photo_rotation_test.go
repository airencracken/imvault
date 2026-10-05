// SPDX-License-Identifier: AGPL-3.0-or-later
package db

import (
	"imvault/internal/testutil"
	"path/filepath"
	"testing"
)

func TestPhotoRotationMigrationPreservesOriginalsAndConstrainsAngles(t *testing.T) {
	database := buildDatabaseBefore(t, filepath.Join(t.TempDir(), "old.db"), "029_photo_rotation.sql")
	defer testutil.Close(t, database)
	if _, err := database.Exec(`INSERT INTO files(id,original_name,ext,mime,size,sha256,created_at,visibility,kind) VALUES
 ('photo','holiday.png','png','image/png',123,'original',456,'private','image'),
 ('clip','clip.mp4','mp4','video/mp4',789,'clip',456,'members','video')`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	var rotation int
	var sha string
	var size int64
	if err := database.QueryRow(`SELECT rotation,sha256,size FROM files WHERE id='photo'`).Scan(&rotation, &sha, &size); err != nil || rotation != 0 || sha != "original" || size != 123 {
		t.Fatal("migration changed original", err)
	}
	for _, angle := range []any{nil, -90, 45, 360, "bad"} {
		if _, err := database.Exec(`UPDATE files SET rotation=? WHERE id='photo'`, angle); err == nil {
			t.Fatal("schema accepted invalid angle", angle)
		}
	}
	for _, angle := range []int{0, 90, 180, 270} {
		if _, err := database.Exec(`UPDATE files SET rotation=? WHERE id='photo'`, angle); err != nil {
			t.Fatal("valid angle rejected", angle, err)
		}
	}
	if _, err := database.Exec(`UPDATE files SET rotation=90 WHERE id='clip'`); err == nil {
		t.Fatal("schema allowed video rotation")
	}
	if err := migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT rotation FROM files WHERE id='photo'`).Scan(&rotation); err != nil || rotation != 270 {
		t.Fatal("repeated migration lost rotation", err)
	}
}
