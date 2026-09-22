// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/maintenance"
	"imvault/internal/media"
	"imvault/internal/storage"
	"imvault/internal/store"
)

// Exercise the real SDK and routes together, including owner-only originals
// and metadata-free public downloads from a private object store.
func useS3(t *testing.T, h *harness) {
	t.Helper()
	disk, err := storage.NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("unsigned object request")
			w.WriteHeader(403)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/private/")
		switch r.Method {
		case "PUT":
			if _, err := disk.Save(r.Context(), key, r.Body); err != nil {
				http.Error(w, err.Error(), 500)
			}
		case "DELETE":
			if err := disk.Delete(r.Context(), key); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.WriteHeader(204)
		case "GET", "HEAD":
			reader, err := disk.Open(r.Context(), key)
			if err != nil {
				w.WriteHeader(404)
				io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
				return
			}
			defer reader.Close()
			w.Header().Set("ETag", `"object"`)
			http.ServeContent(w, r, key, time.Time{}, reader)
		default:
			http.Error(w, "unsupported", 400)
		}
	}))
	t.Cleanup(endpoint.Close)
	objects, err := storage.NewS3(t.Context(), config.Storage{Driver: "s3", Endpoint: endpoint.URL, Bucket: "private", Region: "us-east-1", PathStyle: true, AccessKey: "test", SecretKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	h.srv.objects = objects
}

func TestS3MaintenanceMigratesBothWaysAndRestoresWithoutRemoteStorage(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	owner := h.sessionFor(t, user.ID)
	id := uploadAs(t, owner, "photo.png", "private")
	source := h.srv.objects
	useS3(t, h)
	remote := h.srv.objects
	if err := maintenance.Migrate(t.Context(), h.store, source, remote, io.Discard); err != nil {
		t.Fatal("disk to S3", err)
	}
	processor := media.NewProcessor(16, 24, 80, 0, nil)
	if err := maintenance.Regenerate(t.Context(), h.store, remote, processor, false, io.Discard); err != nil {
		t.Fatal("remote regeneration", err)
	}
	if resp, _ := owner.get("/f/" + id + "/thumb"); resp.StatusCode != 200 {
		t.Fatal("rebuilt remote thumbnail unreadable")
	}
	disk, err := storage.NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Migrate(t.Context(), h.store, remote, disk, io.Discard); err != nil {
		t.Fatal("S3 to disk", err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot")
	cfg := *h.srv.cfg
	cfg.SecretKeyFile = filepath.Join(cfg.DataDir, "secret.key")
	if err := maintenance.Backup(t.Context(), &cfg, h.store, remote, backup, io.Discard); err != nil {
		t.Fatal("S3 backup", err)
	}
	output := filepath.Join(t.TempDir(), "restored")
	if err := maintenance.Restore(t.Context(), backup, output, io.Discard); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(t.Context(), filepath.Join(output, "imvault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	file, err := store.New(database).FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := storage.OpenDisk(filepath.Join(output, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{file.ObjectKey, file.ThumbKey, file.PreviewKey} {
		if _, err := objects.Stat(t.Context(), key); err != nil {
			t.Fatal("remote backup did not restore media locally", err)
		}
	}
}

func TestS3RoutesPreserveVisibilityExportsAndRanges(t *testing.T) {
	h := newHarness(t)
	useS3(t, h)
	user := h.seedUser("alice")
	owner := h.sessionFor(t, user.ID)
	id := uploadAs(t, owner, "photo.png", "private")
	guest := h.newSession(t)
	for _, path := range []string{"/f/" + id, "/f/" + id + "/raw", "/f/" + id + "/thumb", "/f/" + id + "/preview"} {
		if resp, _ := guest.get(path); resp.StatusCode != 404 {
			t.Fatalf("S3 private file exposed at %s: %d", path, resp.StatusCode)
		}
	}
	resp, full := owner.get("/f/" + id + "/raw")
	if resp.StatusCode != 200 {
		t.Fatal("original unreadable")
	}
	req, err := http.NewRequest("GET", h.server.URL+"/f/"+id+"/raw", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=4-15")
	response, err := owner.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 206 || !bytes.Equal(data, []byte(full)[4:16]) {
		t.Fatalf("S3 range failed: %d %v", response.StatusCode, err)
	}
	_, entries, _ := readExport(t, owner)
	if !bytes.Equal(entries["files/"+id+"/photo.png"], []byte(full)) {
		t.Fatal("S3 account export lost original")
	}
	if resp, _ := owner.post("/f/"+id+"/visibility", url.Values{"visibility": {"public"}}); resp.StatusCode != 303 {
		t.Fatal("visibility edit failed")
	}
	if resp, _ := guest.get("/f/" + id + "/thumb"); resp.StatusCode != 200 {
		t.Fatal("public S3 thumbnail unreadable")
	}
	if resp, _ := guest.get("/f/" + id + "/raw"); resp.StatusCode != 200 {
		t.Fatal("metadata-free public copy unreadable")
	}
	if resp, _ := owner.post("/f/"+id+"/delete", url.Values{}); resp.StatusCode != 303 {
		t.Fatal("S3 deletion failed")
	}
	if resp, _ := owner.get("/f/" + id + "/raw"); resp.StatusCode != 404 {
		t.Fatal("deleted S3 file still visible")
	}
}
