// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/storage"
	"imvault/internal/store"
)

type failingStorage struct {
	storage.Backend
	deleteFails, renditionFails, statFails bool
	writes                                 int
}

func (f *failingStorage) Delete(ctx context.Context, key string) error {
	if f.deleteFails {
		return errors.New("storage is unreachable")
	}
	return f.Backend.Delete(ctx, key)
}
func (f *failingStorage) Save(ctx context.Context, key string, r io.Reader) (int64, error) {
	f.writes++
	if f.renditionFails && strings.HasPrefix(key, "thumb/") {
		return 0, errors.New("write failed")
	}
	return f.Backend.Save(ctx, key, r)
}
func (f *failingStorage) Stat(ctx context.Context, key string) (int64, error) {
	if f.statFails {
		return 0, errors.New("permission denied")
	}
	return f.Backend.Stat(ctx, key)
}

func TestFailedObjectDeletionIsRetriedByCleanup(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	owner := h.sessionFor(t, user.ID)
	id := uploadAs(t, owner, "photo.png", "private")
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	failing := &failingStorage{Backend: h.srv.objects, deleteFails: true}
	h.srv.objects = failing
	if resp, _ := owner.post("/f/"+id+"/delete", url.Values{}); resp.StatusCode != 303 {
		t.Fatal("delete route failed")
	}
	blob, err := h.store.BlobBySHA(t.Context(), file.SHA256)
	if err != nil || !blob.Orphaned() {
		t.Fatal("failed deletion lost the retry record")
	}
	failing.deleteFails = false
	h.srv.sweepOrphanedBlobs(t.Context())
	if _, err := h.store.BlobBySHA(t.Context(), file.SHA256); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("cleanup did not finish deletion", err)
	}
	if _, err := failing.Stat(t.Context(), file.ObjectKey); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("cleanup retained original bytes", err)
	}
}

func TestFailedRenditionDoesNotDeleteSharedOriginal(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	owner := h.sessionFor(t, user.ID)
	id := uploadAs(t, owner, "first.png", "private")
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	h.srv.cfg.ThumbMax++ // Requires renditions for the new settings.
	h.srv.objects = &failingStorage{Backend: h.srv.objects, renditionFails: true}
	_, body := owner.upload(map[string]string{"visibility": "private"}, []uploadFile{{name: "copy.png", data: pngFixture(t, 32, 32)}})
	if !strings.Contains(body, "could not store the renditions") {
		t.Fatalf("expected failed rendition: %s", body)
	}
	if _, err := h.srv.objects.Stat(t.Context(), file.ObjectKey); err != nil {
		t.Fatal("failed duplicate upload deleted a shared original", err)
	}
	if _, err := h.srv.objects.Stat(t.Context(), file.ThumbKey); err != nil {
		t.Fatal("failed duplicate upload deleted a shared thumbnail", err)
	}
}

func TestStoragePermissionFailureDoesNotTriggerOverwrite(t *testing.T) {
	h := newHarness(t)
	failing := &failingStorage{Backend: h.srv.objects, statFails: true}
	h.srv.objects = failing
	if err := h.srv.storeObject(t.Context(), "orig/example", strings.NewReader("data")); err == nil {
		t.Fatal("storage failure hidden")
	}
	if failing.writes != 0 {
		t.Fatal("permission failure mistaken for absence")
	}
}

func TestContentLockCancellationReleasesItsReference(t *testing.T) {
	var locks contentLocks
	unlock, err := locks.acquire(t.Context(), "same")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if release, err := locks.acquire(ctx, "same"); err == nil {
		release()
		t.Fatal("cancelled waiter acquired content")
	}
	other, err := locks.acquire(t.Context(), "different")
	if err != nil {
		t.Fatal(err)
	}
	other()
	unlock()
	if len(locks.entries) != 0 {
		t.Fatal("content locks accumulated after work finished")
	}
}
