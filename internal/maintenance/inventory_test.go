// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"testing"

	"imvault/internal/models"
)

func TestInventoryRejectsMissingAndConflictingOriginalKeys(t *testing.T) {
	f := newFixture(t)
	if _, err := f.store.DB().Exec(`UPDATE blobs SET object_key = ''`); err != nil {
		t.Fatal(err)
	}
	if _, err := Inventory(t.Context(), f.store); err == nil {
		t.Fatal("missing original key omitted silently")
	}
	if _, err := f.store.DB().Exec(`UPDATE blobs SET object_key = 'orig/shared.png'`); err != nil {
		t.Fatal(err)
	}
	if err := f.store.EnsureBlob(t.Context(), "different-hash", 123, "orig/shared.png", "", "", ""); err != nil {
		t.Fatal(err)
	}
	// Only content something refers to is inventoried, so the conflict has to
	// be referenced to matter.
	if err := f.store.CreateFile(t.Context(), &models.File{ID: "other", OriginalName: "x.png", SHA256: "different-hash", Size: 123}); err != nil {
		t.Fatal(err)
	}
	if _, err := Inventory(t.Context(), f.store); err == nil {
		t.Fatal("conflicting originals accepted")
	}
}

func TestInventoryLeavesUnreferencedContentToTheSweep(t *testing.T) {
	f := newFixture(t)
	// An orphan whose deletion stopped halfway: its record survives, its
	// original does not.
	if err := f.store.EnsureBlob(t.Context(), "orphan-hash", 9, "orig/gone.png", "thumb/gone.png", "", ""); err != nil {
		t.Fatal(err)
	}
	entries, err := Inventory(t.Context(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Key == "orig/gone.png" || entry.Key == "thumb/gone.png" {
			t.Fatalf("unreferenced content was inventoried: %+v", entry)
		}
	}
}
