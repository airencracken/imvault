// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"imvault/internal/config"
)

// uploadOneAs uploads a single file through the browser uploader and returns
// the stored record.
func uploadOneAs(t *testing.T, s *session, name string, data []byte) (*http.Response, string) {
	t.Helper()

	return s.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: name, data: data},
	})
}

func TestIdenticalUploadsShareTheirBytes(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	image := pngFixture(t, 64, 64)

	resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "first.png", data: image},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first upload = %d", resp.StatusCode)
	}
	firstID := firstFileID(t, body)

	resp, body = h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "second.png", data: image},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second upload = %d", resp.StatusCode)
	}
	secondID := firstFileID(t, body)

	if firstID == secondID {
		t.Fatal("the two uploads produced one row; they should be two files")
	}

	first, err := h.store.FileByID(t.Context(), firstID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.store.FileByID(t.Context(), secondID)
	if err != nil {
		t.Fatal(err)
	}

	// Two files, one set of bytes.
	if first.ObjectKey != second.ObjectKey {
		t.Errorf("identical content was stored twice:\n  %s\n  %s", first.ObjectKey, second.ObjectKey)
	}
	if first.ThumbKey != second.ThumbKey || first.PreviewKey != second.PreviewKey {
		t.Error("renditions were not shared")
	}
	if objects := countStoredObjects(t, h.dataDir); objects != 3 {
		t.Errorf("%d objects on disk, want 3 (original, thumbnail, preview)", objects)
	}

	// The names are still each file's own.
	if first.OriginalName != "first.png" || second.OriginalName != "second.png" {
		t.Errorf("names were conflated: %q and %q", first.OriginalName, second.OriginalName)
	}

	// And both still serve.
	for _, id := range []string{firstID, secondID} {
		if resp, _ := h.get("/f/" + id + "/raw"); resp.StatusCode != http.StatusOK {
			t.Errorf("raw for %s = %d, want 200", id, resp.StatusCode)
		}
	}
}

func TestDeletingOneCopyKeepsTheSharedBytes(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	image := pngFixture(t, 64, 64)

	_, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "one.png", data: image},
	})
	firstID := firstFileID(t, body)

	_, body = h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "two.png", data: image},
	})
	secondID := firstFileID(t, body)

	// Remove the first. The second must be untouched, which is the whole point
	// of checking for other referrers before deleting anything.
	h.postForm("/f/"+firstID+"/delete", url.Values{
		"csrf_token": {h.csrf()},
		"next":       {"/gallery"},
	})

	if _, err := h.store.FileByID(t.Context(), firstID); err == nil {
		t.Error("the deleted file is still in the database")
	}
	if resp, _ := h.get("/f/" + secondID + "/raw"); resp.StatusCode != http.StatusOK {
		t.Errorf("the surviving copy's bytes were removed: %d", resp.StatusCode)
	}
	// Three for the shared content — the original, the thumbnail, and the
	// preview — plus the metadata-free copy the public fetch above produced.
	// That copy belongs to the content, so it survives while either file does.
	if objects := countStoredObjects(t, h.dataDir); objects != 4 {
		t.Errorf("%d objects remain, want 4: the second file still needs them", objects)
	}

	// Deleting the last referrer does remove them.
	h.postForm("/f/"+secondID+"/delete", url.Values{
		"csrf_token": {h.csrf()},
		"next":       {"/gallery"},
	})
	if objects := countStoredObjects(t, h.dataDir); objects != 0 {
		t.Errorf("%d objects remain after the last copy went", objects)
	}
}

func TestDifferentImagesAreStoredSeparately(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	_, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "one.png", data: pngFixture(t, 64, 64)},
	})
	firstID := firstFileID(t, body)

	_, body = h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "two.png", data: pngFixture(t, 32, 32)},
	})
	secondID := firstFileID(t, body)

	first, _ := h.store.FileByID(t.Context(), firstID)
	second, _ := h.store.FileByID(t.Context(), secondID)

	if first.SHA256 == second.SHA256 {
		t.Fatal("two different images produced the same hash")
	}
	if first.ObjectKey == second.ObjectKey {
		t.Error("two different images were stored under one key")
	}
	if objects := countStoredObjects(t, h.dataDir); objects != 6 {
		t.Errorf("%d objects on disk, want 6", objects)
	}
}

func TestDeduplicationAcrossAccounts(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	// A second account, so the two uploads have different owners.
	alice := h.seedUser("alice")
	if err := h.store.SetPassword(t.Context(), alice.ID,
		mustHashPassword(t, testPassword)); err != nil {
		t.Fatal(err)
	}
	aliceSession := h.sessionFor(t, alice.ID)

	image := pngFixture(t, 48, 48)

	// The administrator uploads it.
	_, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "admin.png", data: image},
	})
	adminID := firstFileID(t, body)

	// And so does alice.
	resp, body := aliceSession.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "alice.png", data: image},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alice's upload = %d", resp.StatusCode)
	}
	aliceID := firstFileID(t, body)

	adminFile, _ := h.store.FileByID(t.Context(), adminID)
	aliceFile, _ := h.store.FileByID(t.Context(), aliceID)

	if adminFile.ObjectKey != aliceFile.ObjectKey {
		t.Error("the same content was stored twice across accounts")
	}
	if objects := countStoredObjects(t, h.dataDir); objects != 3 {
		t.Errorf("%d objects on disk, want 3", objects)
	}

	// Each account is charged for its own file: deduplication saves disk, not
	// quota, because each of them really does have a file.
	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	if boss.StorageUsed != adminFile.Size {
		t.Errorf("the administrator is charged %d, want %d", boss.StorageUsed, adminFile.Size)
	}
	// Re-read, because the struct from seedUser is a snapshot from before the
	// upload happened.
	aliceNow, err := h.store.UserByID(t.Context(), alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if aliceNow.StorageUsed != aliceFile.Size {
		t.Errorf("alice is charged %d, want %d", aliceNow.StorageUsed, aliceFile.Size)
	}

	// The administrator deleting their copy must leave alice's alone.
	h.postForm("/f/"+adminID+"/delete", url.Values{
		"csrf_token": {h.csrf()},
		"next":       {"/gallery"},
	})
	if resp, _ := h.get("/f/" + aliceID + "/raw"); resp.StatusCode != http.StatusOK {
		t.Errorf("one account's delete removed another's bytes: %d", resp.StatusCode)
	}
}

func TestExpiringAnonymousUploadDoesNotStrandAnOwnersCopy(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		// Expire anonymous uploads immediately, so one cleanup pass is enough.
		cfg.AnonymousTTL = time.Nanosecond
	})
	h.registerForm("marcus")

	image := pngFixture(t, 40, 40)

	// The account uploads it first, which owns the bytes.
	_, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "mine.png", data: image},
	})
	ownedID := firstFileID(t, body)

	// Then the same content arrives anonymously, which shares those bytes.
	anon := h.newSession(t)
	resp, body := anon.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "anon.png", data: image},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous upload = %d", resp.StatusCode)
	}
	anonID := firstFileID(t, body)

	owned, _ := h.store.FileByID(t.Context(), ownedID)
	anonFile, _ := h.store.FileByID(t.Context(), anonID)
	if owned.ObjectKey != anonFile.ObjectKey {
		t.Fatal("the anonymous upload did not share the bytes")
	}

	// The reaper removes the anonymous row, and must not take the bytes with
	// it: the owner's file still points at them.
	h.srv.runCleanup(t.Context())

	if _, err := h.store.FileByID(t.Context(), anonID); err == nil {
		t.Fatal("the expired anonymous upload survived the reaper")
	}
	if resp, _ := h.get("/f/" + ownedID + "/raw"); resp.StatusCode != http.StatusOK {
		t.Errorf("the reaper removed bytes an owner still needs: %d", resp.StatusCode)
	}
	// As above: three for the content, plus the metadata-free copy that the
	// owner's public fetch produced, which must not have been stranded by the
	// anonymous copy expiring.
	if objects := countStoredObjects(t, h.dataDir); objects != 4 {
		t.Errorf("%d objects remain, want 4", objects)
	}
}

func TestRenditionSettingsArePartOfTheKey(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	image := pngFixture(t, 64, 64)
	_, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "one.png", data: image},
	})
	first, err := h.store.FileByID(t.Context(), firstFileID(t, body))
	if err != nil {
		t.Fatal(err)
	}

	// A different thumbnail bound has to produce a different key, or the new
	// file would be handed a rendition at the old size.
	original := h.srv.renditionTag()
	before := h.srv.contentKeys(first.SHA256, "png", "jpg", "jpg")

	h.srv.cfg.ThumbMax = h.srv.cfg.ThumbMax * 2
	if h.srv.renditionTag() == original {
		t.Fatal("the rendition tag does not depend on the settings")
	}

	after := h.srv.contentKeys(first.SHA256, "png", "jpg", "jpg")
	if before.thumb == after.thumb {
		t.Error("the thumbnail key did not change with the settings")
	}
	// The original is content alone, so it must not move.
	if before.object != after.object {
		t.Error("the original's key changed with the settings")
	}
}

func TestUnchangedContentIsNotRewritten(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	image := pngFixture(t, 64, 64)

	_, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "one.png", data: image},
	})
	first, err := h.store.FileByID(t.Context(), firstFileID(t, body))
	if err != nil {
		t.Fatal(err)
	}

	key, ok := h.srv.objects.LocalPath(first.ObjectKey)
	if !ok {
		t.Skip("the harness is not using a disk backend")
	}
	info, err := statModTime(key)
	if err != nil {
		t.Fatal(err)
	}

	// A second upload of the same content should reuse the metadata rather than
	// decoding again, which means the object is not touched at all.
	time.Sleep(10 * time.Millisecond)
	h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "two.png", data: image},
	})

	after, err := statModTime(key)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Equal(info) {
		t.Error("the stored object was rewritten for identical content")
	}
}

func TestDeduplicatedUploadsStillRejectBadContent(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	// The same rubbish twice: neither is stored, and the rejection is the same
	// both times rather than the second being mistaken for a duplicate.
	for i := 0; i < 2; i++ {
		resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
			{name: "notes.txt", data: []byte("this is not an image")},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d = %d, want a 200 carrying the error", i+1, resp.StatusCode)
		}
		if !strings.Contains(body, "notes.txt") {
			t.Errorf("attempt %d did not report the failure: %s", i+1, truncate(body))
		}
	}

	if objects := countStoredObjects(t, h.dataDir); objects != 0 {
		t.Errorf("%d objects were written for rejected uploads", objects)
	}
}
