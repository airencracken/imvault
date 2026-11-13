// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/metadata"
	"imvault/internal/models"
	"imvault/internal/storage"
	"imvault/internal/store"
)

func TestIndependentLocationPagesDownloadsAndDedup(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "test", nil)
	owner := h.sessionFor(t, user.ID)
	member := h.sessionFor(t, h.seedUser("bob").ID)
	guest := h.newSession(t)
	original := gpsUploadFixture(t, false)
	for _, choice := range []struct {
		exif, location string
		camera, gps    bool
	}{
		{"shown", "hidden", true, false}, {"hidden", "shown", false, true},
		{"", "", false, false}, {"shown", "", true, false}, {"true", "1", true, true},
	} {
		t.Run(choice.exif+"/"+choice.location, func(t *testing.T) {
			fields := map[string]string{"visibility": "public"}
			if choice.exif != "" {
				fields["metadata"] = choice.exif
			}
			if choice.location != "" {
				fields["location"] = choice.location
			}
			resp, raw := h.apiUpload(key, fields, map[string][]byte{"photo.jpg": original})
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("upload: %d %s", resp.StatusCode, raw)
			}
			file := decodeUpload(t, raw).Files[0]
			if file.Location != models.ShownWord(choice.gps) || file.Metadata != models.ShownWord(choice.camera) || file.Details.Location() == "" {
				t.Fatalf("upload contract: %+v", file)
			}
			for _, viewer := range []*session{guest, member, owner} {
				_, page := viewer.get("/f/" + file.ID)
				if strings.Contains(page, "TestCam One") != (choice.camera || viewer == owner) || strings.Contains(page, "51.50740") != (choice.gps || viewer == owner) {
					t.Fatalf("page visibility camera=%v gps=%v owner=%v", choice.camera, choice.gps, viewer == owner)
				}
			}
			resp, served := guest.getBytes("/f/" + file.ID + "/raw")
			if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Cache-Control"), "no-cache") {
				t.Fatalf("download: %d %v", resp.StatusCode, resp.Header)
			}
			details, _ := metadata.Extract(bytes.NewReader(served))
			if details == nil {
				details = &metadata.Details{}
			}
			if (details.Camera != "") != choice.camera || (details.Location() != "") != choice.gps {
				t.Fatalf("download metadata: %+v", details)
			}
			if !bytes.Equal(storedOriginal(t, h, file.ID), original) {
				t.Fatal("original modified")
			}
		})
	}
}

func TestLocationAPIValidationAndAtomicity(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	key := h.seedKey(u.ID, "test", nil)
	id := h.uploadOne(key, "photo.png", 20, 20)
	path := "/api/v1/files/" + id
	before, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"location", "metadata"} {
		for _, invalid := range []any{"", "public", "inherit", nil, 42, []string{"hidden"}, map[string]string{"policy": "hidden"}, "shown\x00", "<script>", strings.Repeat("shown", 1000)} {
			resp, raw := h.apiJSON(http.MethodPatch, path, key, map[string]any{field: invalid, "description": "must not save", "visibility": "public"})
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("invalid %s %v: %d %s", field, invalid, resp.StatusCode, raw)
			}
			f, err := h.store.FileByID(t.Context(), id)
			if err != nil || f.Description != "" || f.ShowLocation != before.ShowLocation || f.ShowCamera != before.ShowCamera || f.Visibility != before.Visibility {
				t.Fatal("invalid update partly saved")
			}
		}
	}
	resp, raw := h.apiJSON(http.MethodPatch, path, key, map[string]any{"metadata": false, "location": true})
	if err := json.Unmarshal(raw, new(apiFileJSON)); err != nil || resp.StatusCode != 200 {
		t.Fatalf("JSON booleans: %d %s", resp.StatusCode, raw)
	}
	if f, _ := h.store.FileByID(t.Context(), id); f.ShowCamera || !f.ShowLocation {
		t.Fatal("JSON booleans not applied")
	}
	resp, raw = h.apiJSON(http.MethodPatch, path, key, map[string]any{"location": "shown"})
	var updated apiFileJSON
	if err := json.Unmarshal(raw, &updated); err != nil || resp.StatusCode != 200 || updated.Location != "shown" {
		t.Fatalf("patch location: %d %s", resp.StatusCode, raw)
	}
	h.apiJSON(http.MethodPatch, path, key, map[string]any{"description": "retained"})
	f, _ := h.store.FileByID(t.Context(), id)
	if !f.ShowLocation {
		t.Fatal("omitted location reset")
	}
	resp, _ = h.apiJSON(http.MethodPatch, path, h.seedKey(h.seedUser("bob").ID, "test", nil), map[string]any{"location": "hidden"})
	if resp.StatusCode != 404 {
		t.Fatal("another owner changed location")
	}
	resp, _ = h.apiUpload(key, map[string]string{"location": "invalid"}, map[string][]byte{"photo.png": pngFixture(t, 20, 20)})
	if resp.StatusCode != 400 {
		t.Fatal("upload accepted invalid location")
	}
	// Albums no longer carry sharing settings, and say so instead of
	// silently ignoring a request to hide something.
	for _, field := range []string{"metadata", "location"} {
		resp, raw = h.apiJSON(http.MethodPost, "/api/v1/albums", key, map[string]any{"title": "Refused", field: "hidden"})
		if resp.StatusCode != 400 || !strings.Contains(string(raw), "set them on each file") {
			t.Fatalf("album %s accepted: %d %s", field, resp.StatusCode, raw)
		}
	}
	resp, raw = h.apiJSON(http.MethodPost, "/api/v1/albums", key, map[string]any{"title": "Trip"})
	album := decodeAlbum(t, raw)
	if resp.StatusCode != 201 || strings.Contains(string(raw), `"location"`) || strings.Contains(string(raw), `"metadata"`) {
		t.Fatalf("album contract: %d %s", resp.StatusCode, raw)
	}
	resp, _ = h.apiJSON(http.MethodPatch, "/api/v1/albums/"+album.Slug, key, map[string]any{"title": "must not save", "location": false})
	persisted, _ := h.store.AlbumBySlug(t.Context(), album.Slug)
	if resp.StatusCode != 400 || persisted.Title != "Trip" {
		t.Fatal("album update with a sharing field partially saved")
	}
}

func TestLocationRouteRequiresOwnerAndCSRF(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	owner := h.sessionFor(t, u.ID)
	id := h.uploadOne(h.seedKey(u.ID, "test", nil), "photo.png", 20, 20)
	path := "/f/" + id + "/location"
	resp, _ := owner.post(path, url.Values{"location": {"shown"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("location route: %d", resp.StatusCode)
	}
	resp, _ = owner.post(path, url.Values{"location": {"invalid"}})
	if resp.StatusCode != 400 {
		t.Fatal("invalid web setting accepted")
	}
	resp, _ = h.sessionFor(t, h.seedUser("bob").ID).post(path, url.Values{"location": {"hidden"}})
	if resp.StatusCode != 403 && resp.StatusCode != 404 {
		t.Fatalf("owner protection: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, h.server.URL+path, strings.NewReader("location=hidden"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := owner.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	mustClose(t, resp.Body)
	if resp.StatusCode != 403 {
		t.Fatalf("CSRF: %d", resp.StatusCode)
	}
	file, _ := h.store.FileByID(t.Context(), id)
	if !file.ShowLocation {
		t.Fatal("rejected request changed policy")
	}
}

func TestLocationMapUsesLocationAndPublicDefault(t *testing.T) {
	var requested []string
	h, _, id := mapHarness(t, &requested, 200)
	shown, hidden := true, false
	for _, choice := range []struct {
		exif, location bool
		status         int
	}{{hidden, shown, 200}, {shown, hidden, 404}} {
		if err := h.store.UpdateFile(t.Context(), id, store.FileUpdate{ShowCamera: &choice.exif, ShowLocation: &choice.location}); err != nil {
			t.Fatal(err)
		}
		status, _, _ := getWith(t, guestClient(), h.server.URL+"/f/"+id+"/map")
		if status != choice.status {
			t.Fatalf("map = %d want %d", status, choice.status)
		}
	}
	// Making a closed file public hides its location again.
	members, public := models.VisibilityMembers, models.VisibilityPublic
	if err := h.store.UpdateFile(t.Context(), id, store.FileUpdate{Visibility: &members, ShowLocation: &shown}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetFileVisibility(t.Context(), id, public); err != nil {
		t.Fatal(err)
	}
	status, _, _ := getWith(t, guestClient(), h.server.URL+"/f/"+id+"/map")
	if status != 404 {
		t.Fatal("making the file public left its location visible")
	}
	if len(requested) != 1 {
		t.Fatalf("hidden location contacted provider: %v", requested)
	}
}

func TestLocationVariantsSurvivePolicyChangesAndMissingCache(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	key := h.seedKey(u.ID, "test", nil)
	_, raw := h.apiUpload(key, map[string]string{"visibility": "public", "metadata": "shown", "location": "hidden"}, map[string][]byte{"photo.jpg": gpsUploadFixture(t, false)})
	id := decodeUpload(t, raw).Files[0].ID
	guest := h.newSession(t)
	_, camera := guest.getBytes("/f/" + id + "/raw")
	h.apiJSON(http.MethodPatch, "/api/v1/files/"+id, key, map[string]any{"metadata": "hidden", "location": "shown"})
	_, gps := guest.getBytes("/f/" + id + "/raw")
	if bytes.Equal(camera, gps) {
		t.Fatal("policy change reused wrong bytes")
	}
	file, _ := h.store.FileByID(t.Context(), id)
	blob, err := h.store.BlobBySHA(t.Context(), file.SHA256)
	if err != nil || blob.LocationKey == "" || blob.CameraKey == "" || blob.LocationKey == blob.CameraKey {
		t.Fatalf("variant cache: %+v %v", blob, err)
	}
	if err := h.srv.objects.Delete(t.Context(), blob.LocationKey); err != nil {
		t.Fatal(err)
	}
	resp, rebuilt := guest.getBytes("/f/" + id + "/raw")
	if resp.StatusCode != 200 || !bytes.Equal(gps, rebuilt) {
		t.Fatal("missing filtered object not rebuilt")
	}
	resp, raw = h.apiJSON(http.MethodDelete, "/api/v1/files/"+id, key, nil)
	var result map[string]string
	if err := json.Unmarshal(raw, &result); err != nil || resp.StatusCode != http.StatusOK || result["status"] != "trashed" || result["id"] != id {
		t.Fatalf("trash response: %d %s (%v)", resp.StatusCode, raw, err)
	}
	if _, err := h.store.TrashedFile(t.Context(), id, u.ID); err != nil {
		t.Fatal("file missing from Trash", err)
	}
	for _, objectKey := range blob.Keys() {
		if _, err := h.srv.objects.Stat(t.Context(), objectKey); err != nil {
			t.Fatalf("Trash lost retained object %s: %v", objectKey, err)
		}
	}
	resp, raw = h.apiJSON(http.MethodDelete, "/api/v1/trash/"+id, key, nil)
	if err := json.Unmarshal(raw, &result); err != nil || resp.StatusCode != http.StatusOK || result["status"] != "deleted" || result["id"] != id {
		t.Fatalf("purge response: %d %s (%v)", resp.StatusCode, raw, err)
	}
	for _, objectKey := range blob.Keys() {
		if _, err := h.srv.objects.Stat(t.Context(), objectKey); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("purged file left object %s: %v", objectKey, err)
		}
	}
}
