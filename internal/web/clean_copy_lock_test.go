// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"testing"
	"time"
)

// Building a metadata-free copy waits for whoever holds the content, so it can
// never race the deletion of the bytes it is made from.
func TestCleanCopyWaitsForTheContentLock(t *testing.T) {
	h := newHarness(t)
	h.get("/")
	id := uploadWith(t, h, map[string]string{"visibility": "public"}, jpegWithLocation(t))
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}

	unlock, err := h.srv.content.acquire(t.Context(), file.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, buildErr := h.srv.cleanObject(ctx, file)
	unlock()

	if buildErr == nil {
		t.Fatal("a clean copy was built while the content was locked")
	}
	blob, err := h.store.BlobBySHA(t.Context(), file.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if blob.CleanKey != "" {
		t.Error("a clean copy was recorded while the content was locked")
	}
}
