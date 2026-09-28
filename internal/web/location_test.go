// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/metadata"
	"imvault/internal/models"
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
		{"hidden", "inherit", false, false}, {"shown", "inherit", true, true},
	} {
		t.Run(choice.exif+"/"+choice.location, func(t *testing.T) {
			resp, raw := h.apiUpload(key, map[string]string{"visibility": "public", "metadata": choice.exif, "location": choice.location}, map[string][]byte{"photo.jpg": original})
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("upload: %d %s", resp.StatusCode, raw)
			}
			file := decodeUpload(t, raw).Files[0]
			if file.Location != choice.location || file.Details.Location() == "" {
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
	for _, invalid := range []any{"", "public", nil, true, 42, []string{"hidden"}, map[string]string{"policy": "hidden"}, "shown\x00", "<script>"} {
		resp, raw := h.apiJSON(http.MethodPatch, path, key, map[string]any{"location": invalid, "description": "must not save"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid %v: %d %s", invalid, resp.StatusCode, raw)
		}
		f, err := h.store.FileByID(t.Context(), id)
		if err != nil || f.Description != "" || f.Location != models.MetadataInherit {
			t.Fatal("invalid update partly saved")
		}
	}
	resp, raw := h.apiJSON(http.MethodPatch, path, key, map[string]any{"location": "shown"})
	var updated apiFileJSON
	if err := json.Unmarshal(raw, &updated); err != nil || resp.StatusCode != 200 || updated.Location != "shown" {
		t.Fatalf("patch location: %d %s", resp.StatusCode, raw)
	}
	h.apiJSON(http.MethodPatch, path, key, map[string]any{"description": "retained"})
	f, _ := h.store.FileByID(t.Context(), id)
	if f.Location != models.MetadataShown {
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
	resp, raw = h.apiJSON(http.MethodPost, "/api/v1/albums", key, map[string]any{"title": "Trip", "metadata": "hidden", "location": "shown"})
	album := decodeAlbum(t, raw)
	if resp.StatusCode != 201 || album.Location != "shown" {
		t.Fatalf("album contract: %d %s", resp.StatusCode, raw)
	}
	resp, raw = h.apiJSON(http.MethodPatch, "/api/v1/albums/"+album.Slug, key, map[string]any{"description": "new"})
	if resp.StatusCode != 200 || decodeAlbum(t, raw).Location != "shown" {
		t.Fatal("album omitted location reset")
	}
	resp, _ = h.apiJSON(http.MethodPatch, "/api/v1/albums/"+album.Slug, key, map[string]any{"title": "must not save", "location": false})
	persisted, _ := h.store.AlbumBySlug(t.Context(), album.Slug)
	if resp.StatusCode != 400 || persisted.Title != "Trip" || persisted.Location != models.MetadataShown {
		t.Fatal("invalid album partially saved")
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
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("CSRF: %d", resp.StatusCode)
	}
	file, _ := h.store.FileByID(t.Context(), id)
	if file.Location != models.MetadataShown {
		t.Fatal("rejected request changed policy")
	}
}

func TestLocationMapUsesLocationAndAlbumCeiling(t *testing.T) {
	var requested []string
	h, boss, id := mapHarness(t, &requested, 200)
	shown, hidden := models.MetadataShown, models.MetadataHidden
	for _, choice := range []struct {
		exif, location models.MetadataPolicy
		status         int
	}{{hidden, shown, 200}, {shown, hidden, 404}} {
		if err := h.store.UpdateFile(t.Context(), id, store.FileUpdate{Metadata: &choice.exif, Location: &choice.location}); err != nil {
			t.Fatal(err)
		}
		status, _, _ := getWith(t, guestClient(), h.server.URL+"/f/"+id+"/map")
		if status != choice.status {
			t.Fatalf("map = %d want %d", status, choice.status)
		}
	}
	if err := h.store.UpdateFile(t.Context(), id, store.FileUpdate{Location: &shown}); err != nil {
		t.Fatal(err)
	}
	a, err := h.store.CreateAlbum(t.Context(), boss.ID, store.AlbumInput{Title: "Trip", Metadata: shown, Location: hidden})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.AddFileToAlbum(t.Context(), a.ID, id); err != nil {
		t.Fatal(err)
	}
	status, _, _ := getWith(t, guestClient(), h.server.URL+"/f/"+id+"/map")
	if status != 404 {
		t.Fatal("album restriction failed")
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
	h.apiJSON(http.MethodDelete, "/api/v1/files/"+id, key, nil)
	for _, objectKey := range blob.Keys() {
		if _, err := h.srv.objects.Stat(t.Context(), objectKey); err == nil {
			t.Fatalf("deleted file left object %s", objectKey)
		}
	}
}
