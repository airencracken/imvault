// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"imvault/internal/db"
	"imvault/internal/models"
	"imvault/internal/storage"
	"imvault/internal/store"
	"imvault/internal/testutil"
)

func TestBackupRestoreKeepsLocationPolicyAndFilteredObjects(t *testing.T) {
	f := newFixture(t)
	shown := models.MetadataShown
	if err := f.store.UpdateFile(t.Context(), f.file.ID, store.FileUpdate{Location: &shown}); err != nil {
		t.Fatal(err)
	}
	for _, camera := range []bool{false, true} {
		key := "orig/location.png"
		if camera {
			key = "orig/camera.png"
		}
		if _, err := f.objects.Save(t.Context(), key, strings.NewReader(key)); err != nil {
			t.Fatal(err)
		}
		if err := f.store.SetBlobFilteredKey(t.Context(), f.file.SHA256, key, camera); err != nil {
			t.Fatal(err)
		}
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
	st := store.New(database)
	file, err := st.FileByID(t.Context(), f.file.ID)
	if err != nil || file.Location != shown || file.Metadata != models.MetadataHidden {
		t.Fatalf("restored sharing: %+v %v", file, err)
	}
	blob, err := st.BlobBySHA(t.Context(), file.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := storage.NewDisk(filepath.Join(destination, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{blob.CameraKey, blob.LocationKey} {
		object, err := objects.Open(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(object)
		testutil.Close(t, object)
		if err != nil || string(data) != key {
			t.Fatal("filtered copy was not restored")
		}
	}
}
