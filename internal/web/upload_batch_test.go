// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strings"
	"testing"

	"imvault/internal/models"
	"imvault/internal/store"
)

func TestUploadOrganizationReportsPartialSuccess(t *testing.T) {
	h := newLibraryHarness(t, nil)
	owner := h.seedUser("alice")
	key := h.seedKey(owner.ID, "upload", nil)
	me := h.sessionFor(t, owner.ID)

	if _, err := h.store.DB().Exec(`CREATE TRIGGER reject_upload_tag BEFORE INSERT ON file_tags
		WHEN (SELECT name FROM tags WHERE id=NEW.tag_id)='Blocked'
		BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	data := pngFixture(t, 12, 17)
	fields := map[string]string{"album": "missing", "tags": "Kept, Blocked, Kept"}
	response, raw := h.apiUpload(key, fields, map[string][]byte{"photo.png": data})
	batch := decodeUpload(t, raw)
	if response.StatusCode != http.StatusCreated || len(batch.Files) != 1 || len(batch.Errors) != 0 || len(batch.Warnings) != 2 {
		t.Fatalf("partial result: %d %s", response.StatusCode, raw)
	}
	file := batch.Files[0]
	if len(file.Tags) != 1 || file.Tags[0].Name != "Kept" || batch.Warnings[0].FileID != file.ID || batch.Warnings[1].FileID != file.ID {
		t.Fatalf("wrong tags or warnings: %s", raw)
	}
	if strings.Contains(string(raw), "fixture failure") {
		t.Fatal("internal database error in API output")
	}
	resp, page := me.upload(map[string]string{"album_id": "99999", "tags": "Blocked"}, []uploadFile{{name: "ordinary.png", data: data}})
	for _, match := range []string{"These files were saved and still need organizing", "Organize this file", "selected album could not be added", "data-warnings=\"2\""} {
		if resp.StatusCode != http.StatusOK || !strings.Contains(page, match) {
			t.Fatalf("missing %q in upload response (%d)", match, resp.StatusCode)
		}
	}
}

func TestSkipDuplicateUploadsAreAccountScoped(t *testing.T) {
	h := newLibraryHarness(t, nil)
	owner, other := h.seedUser("alice"), h.seedUser("other")
	aliceKey, otherKey := h.seedKey(owner.ID, "upload", nil), h.seedKey(other.ID, "upload", nil)
	data := map[string][]byte{"photo.png": pngFixture(t, 12, 17)}
	fields := map[string]string{"skip_duplicates": "1", "visibility": "private"}

	create := func(key string) string {
		t.Helper()
		response, raw := h.apiUpload(key, fields, data)
		batch := decodeUpload(t, raw)
		if response.StatusCode != http.StatusCreated || len(batch.Files) != 1 || len(batch.Skipped) != 0 {
			t.Fatalf("expected a new upload: %d %s", response.StatusCode, raw)
		}
		return batch.Files[0].ID
	}
	old := create(aliceKey)
	if got := create(otherKey); got == old {
		t.Fatal("another account's file was returned")
	}

	resp, raw := h.apiUpload(aliceKey, map[string]string{"skip_duplicates": "1", "visibility": "public", "tags": "unwanted"}, data)
	skipped := decodeUpload(t, raw)
	if resp.StatusCode != http.StatusOK || len(skipped.Files) != 0 || len(skipped.Skipped) != 1 || skipped.Skipped[0].FileID != old || skipped.Skipped[0].Name != "photo.png" {
		t.Fatalf("wrong duplicate contract: %d %s", resp.StatusCode, raw)
	}
	file, err := h.store.FileByID(t.Context(), old)
	if err != nil || file.Visibility != models.VisibilityPrivate || len(file.Tags) != 0 {
		t.Fatalf("skipping changed the existing file: %+v %v", file, err)
	}
	me := h.sessionFor(t, owner.ID)
	htmlResponse, page := me.upload(map[string]string{"skip_duplicates": "1"}, []uploadFile{{name: "again.png", data: data["photo.png"]}})
	if htmlResponse.StatusCode != http.StatusOK || !strings.Contains(page, `data-skipped="1"`) ||
		!strings.Contains(page, `href="/f/`+old+`">again.png`) {
		t.Fatal("browser response lost the skipped file")
	}
	total, err := h.store.CountFiles(t.Context(), store.FileQuery{OwnerID: &owner.ID})
	if err != nil || total != 1 {
		t.Fatalf("skipping created a row: %d %v", total, err)
	}
	resp, raw = h.apiJSON(http.MethodDelete, "/api/v1/files/"+old, aliceKey, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trash: %d %s", resp.StatusCode, raw)
	}
	if got := create(aliceKey); got == old {
		t.Fatal("a file in Trash prevented a new upload")
	}
}

func TestPhoneStillUploadReturnsFormatGuidance(t *testing.T) {
	h := newLibraryHarness(t, nil)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "upload", nil)
	resp, raw := h.apiUpload(key, map[string]string{"skip_duplicates": "yes"}, map[string][]byte{"photo.png": pngFixture(t, 12, 17)})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "Choose whether to skip identical files") {
		t.Fatalf("malformed duplicate preference: %d %s", resp.StatusCode, raw)
	}
	for _, brand := range []string{"heic", "avif"} {
		data := []byte("\x00\x00\x00\x10ftyp" + brand + "\x00\x00\x00\x00")
		response, raw := h.apiUpload(key, nil, map[string][]byte{"renamed.mp4": data})
		if response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "converted to JPEG, PNG or WebP") {
			t.Fatalf("%s went through the clip pipeline: %d %s", brand, response.StatusCode, raw)
		}
	}
}
