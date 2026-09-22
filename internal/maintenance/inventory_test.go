// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import "testing"

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
	if _, err := Inventory(t.Context(), f.store); err == nil {
		t.Fatal("conflicting originals accepted")
	}
}
