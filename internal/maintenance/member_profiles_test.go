// SPDX-License-Identifier: AGPL-3.0-or-later
package maintenance

import (
	"io"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/airencracken/comfylib/memberprofile"
	"imvault/internal/db"
	"imvault/internal/store"
	"imvault/internal/testutil"
)

func TestBackupRestorePreservesOptionalMemberProfile(t *testing.T) {
	f := newFixture(t)
	want := memberprofile.Profile{Name: "Alice", Bio: "A short bio.", Links: []memberprofile.Link{{Label: "Site", URL: "https://example.org/"}}}
	if err := f.store.SetMemberProfile(t.Context(), *f.file.UserID, want); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot")
	if err := Backup(t.Context(), f.cfg, f.store, f.objects, backup, io.Discard); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored")
	if err := Restore(t.Context(), backup, destination, io.Discard); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(t.Context(), filepath.Join(destination, "imvault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, database)
	got, err := store.New(database).MemberProfile(t.Context(), *f.file.UserID)
	if err != nil || got.Username != "alice" || !reflect.DeepEqual(got.Biography, want) {
		t.Fatal("restore changed profile", got, err)
	}
}
