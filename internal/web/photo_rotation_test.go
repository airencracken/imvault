// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"bytes"
	"encoding/json"
	"image"
	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/maintenance"
	"imvault/internal/store"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"imvault/internal/models"
)

func TestPhotoRotationRoutesRenditionsOriginalAndCache(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	owner := h.seedUser("alex")
	session := h.sessionFor(t, owner.ID)
	source := pngFixture(t, 30, 20)
	response, body := session.upload(map[string]string{"visibility": "private"}, []uploadFile{{name: "holiday.png", data: source}})
	if response.StatusCode != 200 {
		t.Fatal(body)
	}
	id := firstFileID(t, body)
	before, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	response, _ = session.post("/f/"+id+"/rotate", url.Values{"direction": {"right"}})
	if response.StatusCode != 303 {
		t.Fatal("rotate route", response.StatusCode)
	}
	after, err := h.store.FileByID(t.Context(), id)
	if err != nil || after.Rotation != 90 || after.ThumbURL() == before.ThumbURL() || after.PreviewURL() == before.PreviewURL() {
		t.Fatal("rotation/cache version", after, err)
	}
	for _, endpoint := range []string{after.ThumbURL(), after.PreviewURL(), "/f/" + id + "/rotated"} {
		resp, err := session.client.Get(h.server.URL + endpoint)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		mustClose(t, resp.Body)
		if err != nil || resp.StatusCode != 200 {
			t.Fatal("rendition", resp.StatusCode, err, string(data))
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width != 20 || config.Height != 30 {
			t.Fatal("rotation geometry", config, err)
		}
		etag := resp.Header.Get("ETag")
		if etag == "" {
			t.Fatal("no cache validator")
		}
		if strings.HasSuffix(endpoint, "/rotated") {
			if resp.Header.Get("Content-Type") != "image/png" || !strings.Contains(resp.Header.Get("Content-Disposition"), "holiday-rotated.png") || !strings.Contains(resp.Header.Get("Cache-Control"), "no-cache") {
				t.Fatal("download contract", resp.Header)
			}
		} else if !strings.Contains(resp.Header.Get("Cache-Control"), "private, max-age=") {
			t.Fatal("private cache", resp.Header)
		}
		// Revalidation must not acquire a decode slot.
		release, ok := h.srv.processing.acquire(t.Context())
		if !ok {
			t.Fatal("take processing slot")
		}
		h.srv.processing.wait = time.Millisecond
		req, err := http.NewRequest("GET", h.server.URL+endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("If-None-Match", "W/"+etag)
		cached, err := session.client.Do(req)
		release()
		if err != nil {
			t.Fatal(err)
		}
		mustClose(t, cached.Body)
		if cached.StatusCode != 304 {
			t.Fatal("revalidation decoded pixels", cached.StatusCode)
		}
	}
	raw, err := session.client.Get(h.server.URL + "/f/" + id + "/raw")
	if err != nil {
		t.Fatal(err)
	}
	original, err := io.ReadAll(raw.Body)
	mustClose(t, raw.Body)
	if err != nil || !bytes.Equal(original, source) {
		t.Fatal("rotation changed original bytes", err)
	}
	_, page := session.get("/f/" + id)
	if !strings.Contains(page, "Download rotated photo") || !strings.Contains(page, after.PreviewURL()) {
		t.Fatal("rotation controls/preview missing")
	}
	response, _ = session.post("/f/"+id+"/rotate", url.Values{"direction": {"reset"}})
	if response.StatusCode != 303 {
		t.Fatal("reset", response.StatusCode)
	}
	reset, _ := h.store.FileByID(t.Context(), id)
	if reset.Rotation != 0 || reset.PreviewURL() != before.PreviewURL() {
		t.Fatal("reset did not restore original preview")
	}
}

func TestPhotoRotationAccessValidationAndAPIContract(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	owner := h.seedUser("alex")
	other := h.seedUser("sam")
	moderator := h.seedUser("mod")
	if err := h.store.SetUserRole(t.Context(), moderator.ID, models.RoleModerator); err != nil {
		t.Fatal(err)
	}
	ownerSession := h.sessionFor(t, owner.ID)
	id := uploadAs(t, ownerSession, "photo.png", "private")
	for _, actor := range []*session{h.sessionFor(t, other.ID), h.sessionFor(t, moderator.ID)} {
		resp, _ := actor.post("/f/"+id+"/rotate", url.Values{"direction": {"right"}})
		if resp.StatusCode != 403 {
			t.Fatal("non-owner rotated photo", resp.StatusCode)
		}
	}
	resp := doForm(t, ownerSession.client, h.server.URL, "/f/"+id+"/rotate", url.Values{"csrf_token": {"wrong"}, "direction": {"right"}})
	if resp.StatusCode != 403 {
		t.Fatal("rotation accepted bad CSRF", resp.StatusCode)
	}
	for _, direction := range []string{"", "90", "<script>", "../right", strings.Repeat("x", 1000)} {
		resp, _ := ownerSession.post("/f/"+id+"/rotate", url.Values{"direction": {direction}})
		if resp.StatusCode != 400 {
			t.Fatal("invalid direction", direction, resp.StatusCode)
		}
	}
	resp, _ = ownerSession.post("/f/"+id+"/rotate", url.Values{"direction": {"left"}})
	if resp.StatusCode != 303 {
		t.Fatal("rotate", resp.StatusCode)
	}
	resp, rawJSON := h.apiDo("GET", "/api/v1/files/"+id, h.seedKey(owner.ID, "rotation-test", nil), nil, "")
	body := string(rawJSON)
	if resp.StatusCode != 200 {
		t.Fatal("API file", resp.StatusCode, body)
	}
	var file apiFileJSON
	if err := json.Unmarshal([]byte(body), &file); err != nil {
		t.Fatal(err)
	}
	if file.Rotation != 270 || !strings.Contains(file.PreviewURL, "/preview?v=") || !strings.HasSuffix(file.RawURL, "/raw") {
		t.Fatal("rotation API contract", file)
	}
	// Reading another person's correction never grants access to the bytes.
	unauthorized := h.sessionFor(t, other.ID)
	for _, suffix := range []string{"preview", "thumb", "rotated"} {
		r, _ := unauthorized.get("/f/" + id + "/" + suffix)
		if r.StatusCode != 404 {
			t.Fatal("private rotation exposed", suffix, r.StatusCode)
		}
	}
}

func TestPhotoRotationSurvivesBackupRestoreAndExport(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) { cfg.SecretKeyFile = filepath.Join(cfg.DataDir, "secret.key") })
	h.provisionAdmin("boss")
	user := h.seedUser("alex")
	owner := h.sessionFor(t, user.ID)
	source := pngFixture(t, 30, 20)
	response, body := owner.upload(map[string]string{"visibility": "private"}, []uploadFile{{name: "holiday.png", data: source}})
	if response.StatusCode != 200 {
		t.Fatal(body)
	}
	id := firstFileID(t, body)
	response, _ = owner.post("/f/"+id+"/rotate", url.Values{"direction": {"right"}})
	if response.StatusCode != 303 {
		t.Fatal(response.StatusCode)
	}
	_, entries, _ := readExport(t, owner)
	var manifest exportManifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 1 || manifest.Files[0].Rotation != 90 || !bytes.Equal(entries[manifest.Files[0].Path], source) {
		t.Fatal("export lost rotation or changed original")
	}
	backup := filepath.Join(t.TempDir(), "snapshot")
	if err := maintenance.Backup(t.Context(), h.srv.cfg, h.store, h.srv.objects, backup, io.Discard); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := maintenance.Restore(t.Context(), backup, restored, io.Discard); err != nil {
		t.Fatal(err)
	}
	database, err := db.OpenCurrent(t.Context(), filepath.Join(restored, "imvault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mustClose(t, database)
	file, err := store.New(database).FileByID(t.Context(), id)
	if err != nil || file.Rotation != 90 {
		t.Fatal("restored photo lost its correction", err)
	}
}

func TestRotatedPhotoDecodeBudgetAndMissingObjects(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	user := h.seedUser("alex")
	owner := h.sessionFor(t, user.ID)
	id := uploadAs(t, owner, "photo.png", "private")
	response, _ := owner.post("/f/"+id+"/rotate", url.Values{"direction": {"right"}})
	if response.StatusCode != 303 {
		t.Fatal(response.StatusCode)
	}
	release, ok := h.srv.processing.acquire(t.Context())
	if !ok {
		t.Fatal("processing slot")
	}
	h.srv.processing.wait = time.Millisecond
	response, _ = owner.get("/f/" + id + "/preview")
	release()
	if response.StatusCode != 503 || response.Header.Get("Retry-After") == "" || response.Header.Get("ETag") != "" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("rotation bypassed shared decode budget", response.StatusCode)
	}
	finish, ok := h.srv.exports.acquire(t.Context())
	if !ok {
		t.Fatal("take download slot")
	}
	h.srv.exports.wait = time.Millisecond
	response, _ = owner.get("/f/" + id + "/rotated")
	finish()
	if response.StatusCode != 503 {
		t.Fatal("full-resolution downloads bypassed serialization", response.StatusCode)
	}
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.srv.objects.Delete(t.Context(), file.PreviewKey); err != nil {
		t.Fatal(err)
	}
	response, body := owner.get("/f/" + id + "/preview")
	if response.StatusCode != 500 || strings.Contains(body, "PNG") {
		t.Fatal("missing source served a partial successful image", response.StatusCode)
	}
}
