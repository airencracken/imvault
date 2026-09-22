// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"imvault/internal/db"
	"imvault/internal/metadata"
	"imvault/internal/models"
	"imvault/internal/storage"
	"imvault/internal/store"
)

func TestRefreshMetadataRepairsExistingUploads(t *testing.T) {
	path := adminTestEnvironment(t)
	database, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	st := store.New(database)
	objects, err := storage.NewDisk(filepath.Join(filepath.Dir(path), "objects"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../internal/metadata/testdata/gps-values-before-directory.jpg")
	if err != nil {
		t.Fatal(err)
	}
	// Cross a maintenance batch boundary. These represent distinct originals;
	// the fixture contents are shared to keep the test small.
	for i := 0; i < 101; i++ {
		sha, key := fmt.Sprintf("%064d", i), fmt.Sprintf("orig/%d.jpg", i)
		if _, err := objects.Save(t.Context(), key, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		if err := st.EnsureBlob(t.Context(), sha, int64(len(data)), key, "", "", `{"camera":"TestCam One"}`); err != nil {
			t.Fatal(err)
		}
	}
	sha := fmt.Sprintf("%064d", 0)
	for _, id := range []string{"first", "duplicate"} {
		if err := st.CreateFile(t.Context(), &models.File{ID: id, SHA256: sha, OriginalName: "photo.jpg", Visibility: models.VisibilityPublic, Metadata: models.MetadataHidden}); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := runCommand([]string{"refresh-metadata"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Checked 101 originals; updated 101.") {
		t.Fatalf("refresh output: %s", &output)
	}
	for _, id := range []string{"first", "duplicate"} {
		file, err := st.FileByID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if metadata.DecodeDetails(file.Details).Location() != "51.50740, -0.12730 (35m)" || file.Metadata != models.MetadataHidden || file.Visibility != models.VisibilityPublic {
			t.Fatalf("refresh lost coordinates or changed privacy: %+v", file)
		}
	}
	stored, err := os.ReadFile(filepath.Join(objects.Root(), "orig/0.jpg"))
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatal("refresh changed the original bytes")
	}
	output.Reset()
	if err := runCommand([]string{"refresh-metadata"}, nil, &output); err != nil || !strings.Contains(output.String(), "updated 0.") {
		t.Fatalf("second refresh: %s (%v)", &output, err)
	}
	// A parser failure leaves known details intact.
	if _, err := objects.Save(t.Context(), "orig/0.jpg", strings.NewReader("not an image")); err != nil {
		t.Fatal(err)
	}
	if err := runCommand([]string{"refresh-metadata"}, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	file, err := st.FileByID(t.Context(), "first")
	if err != nil || metadata.DecodeDetails(file.Details).Location() == "" {
		t.Fatal("parser failure erased stored details")
	}
	if err := objects.Delete(t.Context(), "orig/0.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := runCommand([]string{"refresh-metadata"}, nil, io.Discard); err == nil {
		t.Fatal("missing original was reported as success")
	}
}

func TestRefreshMetadataRequiresAnExistingDatabaseAndNoArguments(t *testing.T) {
	path := adminTestEnvironment(t)
	for _, args := range [][]string{{"refresh-metadata"}, {"refresh-metadata", "extra"}, {"refresh-metadata", "--unknown"}} {
		if err := runCommand(args, nil, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("maintenance created a new database")
	}
	if err := runCommand([]string{"refresh-metadata", "--help"}, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
}
