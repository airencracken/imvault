// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"imvault/internal/db"
	"imvault/internal/storage"
	"imvault/internal/testutil"
)

const futureMigration = "999_from_a_newer_imvault.sql"

func recordFutureMigration(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 0)`, futureMigration); err != nil {
		t.Fatal(err)
	}
}

// Backup validates its own snapshot, so even a caller that skipped the
// command's schema check cannot publish a backup of a newer database.
func TestBackupRefusesADatabaseFromANewerImvault(t *testing.T) {
	f := newFixture(t)
	recordFutureMigration(t, f.store.DB())
	output := filepath.Join(t.TempDir(), "backup")
	err := Backup(t.Context(), f.cfg, f.store, f.objects, output, io.Discard)
	var newer *db.NewerSchemaError
	if !errors.As(err, &newer) {
		t.Fatalf("backup of a newer database: %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("a refused backup was published")
	}
}

// A backup taken by a newer Imvault is refused by an older restore before any
// object is copied, rather than producing a directory its server will refuse.
func TestRestoreRefusesABackupFromANewerImvault(t *testing.T) {
	f := newFixture(t)
	backup := filepath.Join(t.TempDir(), "snapshot")
	if err := Backup(t.Context(), f.cfg, f.store, f.objects, backup, io.Discard); err != nil {
		t.Fatal(err)
	}
	snapshot, err := sql.Open("sqlite", "file:"+filepath.Join(backup, "imvault.db"))
	if err != nil {
		t.Fatal(err)
	}
	recordFutureMigration(t, snapshot)
	testutil.Close(t, snapshot)
	// Re-seal the manifest, as the newer binary would have written it.
	manifest, err := readManifest(backup)
	if err != nil {
		t.Fatal(err)
	}
	root, err := storage.OpenDisk(backup)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Database, err = Fingerprint(t.Context(), root, "imvault.db"); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "restored")
	var progress countingWriter
	err = Restore(t.Context(), backup, destination, &progress)
	var newer *db.NewerSchemaError
	if !errors.As(err, &newer) {
		t.Fatalf("restore of a newer backup: %v", err)
	}
	if progress != 0 {
		t.Fatal("objects were copied before the schema was checked")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("a refused restore was published")
	}
}

type countingWriter int

func (c *countingWriter) Write(p []byte) (int, error) {
	*c += countingWriter(len(p))
	return len(p), nil
}
