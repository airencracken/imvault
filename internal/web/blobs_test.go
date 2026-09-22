// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/url"
	"testing"

	"imvault/internal/store"
)

func TestReferenceCountTracksSharedContent(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	image := pngFixture(t, 40, 40)

	_, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "one.png", data: image},
	})
	firstID := firstFileID(t, body)

	first, err := h.store.FileByID(t.Context(), firstID)
	if err != nil {
		t.Fatal(err)
	}

	blob, err := h.store.BlobBySHA(t.Context(), first.SHA256)
	if err != nil {
		t.Fatalf("no blob was recorded for the upload: %v", err)
	}
	if blob.Refcount != 1 {
		t.Errorf("refcount = %d after one upload, want 1", blob.Refcount)
	}
	if blob.ObjectKey != first.ObjectKey {
		t.Errorf("the blob's key does not match the file's: %q vs %q", blob.ObjectKey, first.ObjectKey)
	}

	// A second identical upload shares the content and bumps the count.
	_, body = h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "two.png", data: image},
	})
	secondID := firstFileID(t, body)

	blob, err = h.store.BlobBySHA(t.Context(), first.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if blob.Refcount != 2 {
		t.Errorf("refcount = %d after two uploads, want 2", blob.Refcount)
	}

	// Deleting one brings it down but keeps the content.
	h.postForm("/f/"+firstID+"/delete", url.Values{
		"csrf_token": {h.csrf()},
		"next":       {"/gallery"},
	})

	blob, err = h.store.BlobBySHA(t.Context(), first.SHA256)
	if err != nil {
		t.Fatal("the content was removed while a file still referred to it")
	}
	if blob.Refcount != 1 {
		t.Errorf("refcount = %d after one delete, want 1", blob.Refcount)
	}
	if resp, _ := h.get("/f/" + secondID + "/raw"); resp.StatusCode != 200 {
		t.Errorf("the surviving file cannot read its bytes: %d", resp.StatusCode)
	}

	// Deleting the last one retires the content and its bytes together.
	h.postForm("/f/"+secondID+"/delete", url.Values{
		"csrf_token": {h.csrf()},
		"next":       {"/gallery"},
	})

	if _, err := h.store.BlobBySHA(t.Context(), first.SHA256); err == nil {
		t.Error("the blob outlived the last file referring to it")
	}
	if objects := countStoredObjects(t, h.dataDir); objects != 0 {
		t.Errorf("%d objects remain after the last reference went", objects)
	}
}

func TestReferenceCountSurvivesACascade(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	alice := h.seedUser("alice")
	if err := h.store.SetPassword(t.Context(), alice.ID,
		mustHashPassword(t, testPassword)); err != nil {
		t.Fatal(err)
	}
	me := h.sessionFor(t, alice.ID)

	image := pngFixture(t, 40, 40)

	// The administrator uploads it, then alice uploads the same bytes, so the
	// content has two owners.
	_, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "admins.png", data: image},
	})
	adminID := firstFileID(t, body)

	resp, _ := me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "alices.png", data: image},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("alice's upload = %d", resp.StatusCode)
	}

	adminFile, err := h.store.FileByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}

	blob, err := h.store.BlobBySHA(t.Context(), adminFile.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if blob.Refcount != 2 {
		t.Fatalf("refcount = %d, want 2", blob.Refcount)
	}

	// Deleting the account removes its files by cascade. The trigger on those
	// deletes is what keeps the count honest: application code never sees the
	// individual rows.
	if _, err := h.store.DB().ExecContext(t.Context(),
		`DELETE FROM users WHERE id = ?`, alice.ID); err != nil {
		t.Fatal(err)
	}

	blob, err = h.store.BlobBySHA(t.Context(), adminFile.SHA256)
	if err != nil {
		t.Fatal("the cascade removed content another account still used")
	}
	if blob.Refcount != 1 {
		t.Errorf("refcount = %d after a cascade delete, want 1", blob.Refcount)
	}

	// The administrator's file is untouched.
	if resp, _ := h.get("/f/" + adminID + "/raw"); resp.StatusCode != 200 {
		t.Errorf("the administrator's file lost its bytes to a cascade: %d", resp.StatusCode)
	}
}

func TestDeletingAnAccountCollectsItsContent(t *testing.T) {
	h := newHarness(t)
	me, userID := accountUnderTest(t, h)

	// Content only this account has, so nothing should survive it.
	resp, body := me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "mine.png", data: pngFixture(t, 40, 40)},
		{name: "other.png", data: pngFixture(t, 24, 24)},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("upload = %d", resp.StatusCode)
	}
	if objects := countStoredObjects(t, h.dataDir); objects == 0 {
		t.Fatal("nothing was written")
	}
	_ = body

	resp, _ = me.post("/settings/account/delete", url.Values{
		"password": {testPassword},
		"confirm":  {"marcus"},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("delete = %d, want 303", resp.StatusCode)
	}

	if _, err := h.store.UserByID(t.Context(), userID); err == nil {
		t.Fatal("the account survived")
	}
	if objects := countStoredObjects(t, h.dataDir); objects != 0 {
		t.Errorf("%d objects were left behind by the account deletion", objects)
	}

	// And nothing is left waiting to be collected either.
	stats, err := h.store.BlobSummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Blobs != 0 {
		t.Errorf("%d content records survived", stats.Blobs)
	}
}

func TestRecomputingReferenceCountsRepairsDrift(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("marcus")

	image := pngFixture(t, 40, 40)
	_, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "one.png", data: image},
	})
	id := firstFileID(t, body)

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the count the way a hard kill part-way through a delete might.
	if _, err := h.store.DB().ExecContext(t.Context(),
		`UPDATE blobs SET refcount = 7 WHERE sha256 = ?`, file.SHA256); err != nil {
		t.Fatal(err)
	}

	resp, page := h.postForm("/admin/maintenance/blobs", url.Values{
		"csrf_token": {h.csrf()},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("recompute = %d, want 303", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); location == "" {
		t.Fatal("no redirect location")
	}
	_ = page

	blob, err := h.store.BlobBySHA(t.Context(), file.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if blob.Refcount != 1 {
		t.Errorf("refcount = %d after the repair, want 1", blob.Refcount)
	}
}

func TestOrphanedContentIsSwept(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	image := pngFixture(t, 40, 40)
	_, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "one.png", data: image},
	})
	id := firstFileID(t, body)

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a deletion path that removed the file row without releasing the
	// bytes: exactly what the sweep exists to catch.
	if _, err := h.store.DB().ExecContext(t.Context(),
		`DELETE FROM files WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	// The trigger brought the count down, so the content is now collectable.
	blob, err := h.store.BlobBySHA(t.Context(), file.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if blob.Refcount != 0 {
		t.Fatalf("refcount = %d after the row vanished, want 0", blob.Refcount)
	}
	if objects := countStoredObjects(t, h.dataDir); objects == 0 {
		t.Fatal("the objects were already gone; nothing for the sweep to do")
	}

	h.srv.sweepOrphanedBlobs(t.Context())

	if objects := countStoredObjects(t, h.dataDir); objects != 0 {
		t.Errorf("%d objects survived the sweep", objects)
	}
	if _, err := h.store.BlobBySHA(t.Context(), file.SHA256); err == nil {
		t.Error("the content record survived the sweep")
	}
}

func TestSharedContentIsNotSwept(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	image := pngFixture(t, 40, 40)
	for _, name := range []string{"one.png", "two.png"} {
		h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
			{name: name, data: image},
		})
	}

	before := countStoredObjects(t, h.dataDir)

	// A sweep with content still in use must be a no-op. Getting this wrong
	// would delete bytes that other files are serving.
	h.srv.sweepOrphanedBlobs(t.Context())

	if after := countStoredObjects(t, h.dataDir); after != before {
		t.Errorf("the sweep removed %d objects that were still in use", before-after)
	}

	user, err := h.store.UserByUsername(t.Context(), "marcus")
	if err != nil {
		t.Fatal(err)
	}
	files, err := h.store.ListFiles(t.Context(), store.FileQuery{OwnerID: &user.ID, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if resp, _ := h.get("/f/" + file.ID + "/raw"); resp.StatusCode != 200 {
			t.Errorf("file %s cannot read its bytes after a sweep: %d", file.ID, resp.StatusCode)
		}
	}
}
