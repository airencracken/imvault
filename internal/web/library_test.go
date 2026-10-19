// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/exifwrite"
	"imvault/internal/maintenance"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"imvault/internal/metadata"
	"imvault/internal/models"
	"imvault/internal/store"
)

func TestTrashHTTPIsolationRestoreExportAndPurge(t *testing.T) {
	h := newLibraryHarness(t, nil)
	owner := h.seedUser("alice")
	other := h.seedUser("bob")
	me := h.sessionFor(t, owner.ID)
	them := h.sessionFor(t, other.ID)
	id := uploadAs(t, me, "holiday.png", "public")
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	album, err := h.store.CreateAlbum(t.Context(), owner.ID, store.AlbumInput{Title: "Holiday", Visibility: models.VisibilityPublic})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.AddFileToAlbum(t.Context(), album.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.AddTag(t.Context(), id, &owner.ID, "Summer"); err != nil {
		t.Fatal(err)
	}
	if resp, _ := me.post("/f/"+id+"/favorite", url.Values{"favorite": {"1"}}); resp.StatusCode != 303 {
		t.Fatal("favorite", resp.StatusCode)
	}
	before, err := h.store.UserByID(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := me.post("/f/"+id+"/delete", url.Values{}); resp.StatusCode != 303 {
		t.Fatal("trash", resp.StatusCode)
	}
	after, err := h.store.UserByID(t.Context(), owner.ID)
	if err != nil || after.StorageUsed != before.StorageUsed {
		t.Fatal("Trash released quota", after, err)
	}
	for _, path := range []string{"/f/" + id, "/f/" + id + "/raw", "/f/" + id + "/thumb", "/f/" + id + "/preview", "/f/" + id + "/rotated", "/f/" + id + "/map", "/p/" + id} {
		for _, session := range []*session{me, them, h.newSession(t)} {
			if resp, _ := session.get(path); resp.StatusCode != 404 {
				t.Errorf("trashed route %s exposed to viewer: %d", path, resp.StatusCode)
			}
		}
	}
	for _, path := range []string{"/gallery", "/recent", "/favorites", "/a/" + album.Slug} {
		if resp, page := me.get(path); resp.StatusCode != 200 || strings.Contains(page, `id="file-`+id+`"`) {
			t.Fatal("listing exposes Trash", path, resp.StatusCode)
		}
	}
	if resp, page := me.get("/trash"); resp.StatusCode != 200 || !strings.Contains(page, "holiday.png") || !strings.Contains(page, "previous sharing settings") {
		t.Fatal("trash page", resp.StatusCode, page)
	}
	if resp, page := them.get("/trash"); resp.StatusCode != 200 || strings.Contains(page, "holiday.png") {
		t.Fatal("foreign trash page", resp.StatusCode)
	}
	if resp, _ := me.get("/trash/" + id + "/thumb"); resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("private trash thumbnail", resp.StatusCode, resp.Header)
	}
	if resp, _ := them.get("/trash/" + id + "/thumb"); resp.StatusCode != 404 {
		t.Fatal("foreign trash thumbnail", resp.StatusCode)
	}
	if resp, _ := them.post("/trash/"+id+"/restore", url.Values{}); resp.StatusCode != 404 {
		t.Fatal("foreign restore", resp.StatusCode)
	}
	_, entries, _ := readExport(t, me)
	var manifest exportManifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 1 || manifest.Files[0].TrashedAt == nil || len(entries[manifest.Files[0].Path]) != int(file.Size) {
		t.Fatal("export omitted trashed original", manifest.Files)
	}
	if len(manifest.Albums) != 1 || !reflect.DeepEqual(manifest.Albums[0].Files, []string{id}) {
		t.Fatal("export omitted trashed membership", manifest.Albums)
	}
	if resp, _ := me.post("/trash/"+id+"/restore", url.Values{}); resp.StatusCode != 303 {
		t.Fatal("restore", resp.StatusCode)
	}
	if resp, _ := them.get("/f/" + id); resp.StatusCode != 200 {
		t.Fatal("sharing not restored", resp.StatusCode)
	}
	if resp, _ := me.post("/trash/"+id+"/purge", url.Values{"confirm": {"yes"}}); resp.StatusCode != 404 {
		t.Fatal("stale purge deleted restored file", resp.StatusCode)
	}
	if resp, _ := me.post("/f/"+id+"/delete", url.Values{}); resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
	}
	if resp, _ := me.post("/trash/"+id+"/purge", url.Values{}); resp.StatusCode != 400 {
		t.Fatal("purge without confirmation", resp.StatusCode)
	}
	if resp, _ := me.post("/trash/"+id+"/purge", url.Values{"confirm": {"yes"}}); resp.StatusCode != 303 {
		t.Fatal("purge", resp.StatusCode)
	}
	after, err = h.store.UserByID(t.Context(), owner.ID)
	if err != nil || after.StorageUsed != 0 {
		t.Fatal("purge retained quota", after, err)
	}
	if n := countStoredObjects(t, h.dataDir); n != 0 {
		t.Fatal("purge retained bytes", n)
	}
}

func TestTrashAPIContractExpiryCleanupAndModeration(t *testing.T) {
	h := newLibraryHarness(t, nil)
	owner := h.seedUser("alice")
	other := h.seedUser("bob")
	key := h.seedKey(owner.ID, "test", nil)
	otherKey := h.seedKey(other.ID, "test", nil)
	id := h.uploadOne(key, "one.png", 32, 32)
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	resp, raw := h.apiJSON(http.MethodDelete, "/api/v1/files/"+id, key, nil)
	var result map[string]string
	if resp.StatusCode != 200 || json.Unmarshal(raw, &result) != nil || result["status"] != "trashed" || result["id"] != id || len(result) != 2 {
		t.Fatal("trash response schema", resp.StatusCode, string(raw))
	}
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/files/"+id, key, nil); resp.StatusCode != 404 {
		t.Fatal("API exposes trashed file", resp.StatusCode)
	}
	resp, raw = h.apiJSON(http.MethodGet, "/api/v1/trash", key, nil)
	var listing struct {
		Files                []trashFileJSON `json:"files"`
		Total, Limit, Offset int
	}
	if resp.StatusCode != 200 || json.Unmarshal(raw, &listing) != nil || len(listing.Files) != 1 || listing.Total != 1 || listing.Files[0].ID != id {
		t.Fatal("trash listing schema", resp.StatusCode, string(raw))
	}
	start, e1 := time.Parse(time.RFC3339, listing.Files[0].TrashedAt)
	end, e2 := time.Parse(time.RFC3339, listing.Files[0].PurgeAfter)
	if e1 != nil || e2 != nil || end.Sub(start) != store.TrashRetention {
		t.Fatal("trash deadline schema", listing.Files)
	}
	for _, method := range []struct{ verb, path string }{{http.MethodPost, "/api/v1/trash/" + id + "/restore"}, {http.MethodDelete, "/api/v1/trash/" + id}} {
		if resp, _ := h.apiJSON(method.verb, method.path, otherKey, nil); resp.StatusCode != 404 {
			t.Fatal("foreign trash action", resp.StatusCode)
		}
	}
	if resp, _ := h.apiJSON(http.MethodPost, "/api/v1/trash/"+id+"/restore", key, nil); resp.StatusCode != 200 {
		t.Fatal("API restore", resp.StatusCode)
	}
	if resp, _ := h.apiJSON(http.MethodDelete, "/api/v1/files/"+id, key, nil); resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	if _, err := h.store.DB().Exec(`UPDATE files SET trashed_at=? WHERE id=?`, time.Now().Add(-store.TrashRetention-time.Minute).Unix(), id); err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.apiJSON(http.MethodPost, "/api/v1/trash/"+id+"/restore", key, nil); resp.StatusCode != 404 {
		t.Fatal("late API restore", resp.StatusCode)
	}
	h.srv.runCleanup(t.Context())
	if _, err := h.store.BlobBySHA(t.Context(), file.SHA256); err == nil {
		t.Fatal("cleanup retained unreferenced content")
	}
	account, err := h.store.UserByID(t.Context(), owner.ID)
	if err != nil || account.StorageUsed != 0 {
		t.Fatal("cleanup retained quota", account, err)
	}
	// Administrator removal stays permanent, with no owner restoration path.
	admin := h.provisionAdmin("boss")
	_ = admin
	id = h.uploadOne(key, "removed.png", 32, 32)
	if resp, _ := h.postForm("/admin/files/"+id+"/delete", url.Values{"csrf_token": {h.csrf()}}); resp.StatusCode != 303 {
		t.Fatal("moderation", resp.StatusCode)
	}
	if resp, _ := h.apiJSON(http.MethodPost, "/api/v1/trash/"+id+"/restore", key, nil); resp.StatusCode != 404 {
		t.Fatal("moderation removal restorable", resp.StatusCode)
	}
}

func TestBulkLibraryHTTPAPIAtomicityAndFilterContinuity(t *testing.T) {
	h := newLibraryHarness(t, nil)
	owner := h.seedUser("alice")
	other := h.seedUser("bob")
	me := h.sessionFor(t, owner.ID)
	key := h.seedKey(owner.ID, "test", nil)
	otherKey := h.seedKey(other.ID, "test", nil)
	first, second, foreign := h.uploadOne(key, "one.png", 32, 32), h.uploadOne(key, "two.png", 40, 40), h.uploadOne(otherKey, "foreign.png", 48, 48)
	album, err := h.store.CreateAlbum(t.Context(), owner.ID, store.AlbumInput{Title: "Holiday"})
	if err != nil {
		t.Fatal(err)
	}
	if resp, page := me.get("/gallery"); resp.StatusCode != 200 || !strings.Contains(page, `formaction="/gallery/album"`) || !strings.Contains(page, `formaction="/gallery/visibility"`) {
		t.Fatal("bulk controls", resp.StatusCode)
	}
	resp := doForm(t, me.client, h.server.URL, "/gallery/album", url.Values{"csrf_token": {h.csrfFor(t, me.client)}, "files": {first, second}, "album": {fmt.Sprint(album.ID)}, "q": {"one&two"}, "sort": {"taken"}, "year": {"2024"}, "month": {"01"}, "page": {"2"}})
	dest, err := resp.Location()
	if err != nil || resp.StatusCode != 303 || dest.Query().Get("sort") != "taken" || dest.Query().Get("year") != "2024" || dest.Query().Get("month") != "01" || dest.Query().Get("q") != "one&two" || dest.Query().Get("page") != "2" {
		t.Fatal("bulk redirect lost filters", dest, err)
	}
	resp, raw := h.apiJSON(http.MethodPost, "/api/v1/library/visibility", key, map[string]any{"files": []string{first, second, first}, "visibility": "members"})
	var result map[string]int
	if resp.StatusCode != 200 || json.Unmarshal(raw, &result) != nil || result["updated"] != 2 || len(result) != 1 {
		t.Fatal("bulk API response schema", resp.StatusCode, string(raw))
	}
	for _, body := range []any{map[string]any{"files": []string{first, foreign}, "visibility": "public"}, map[string]any{"files": []string{first, "missing"}, "visibility": "public"}, map[string]any{"files": []string{first}, "visibility": "oops"}, map[string]any{"files": []any{first, 42}, "visibility": "public"}} {
		resp, raw = h.apiJSON(http.MethodPost, "/api/v1/library/visibility", key, body)
		if resp.StatusCode != 400 && resp.StatusCode != 404 {
			t.Fatal("invalid bulk API", resp.StatusCode, string(raw))
		}
	}
	file, err := h.store.FileByID(t.Context(), first)
	if err != nil || file.Visibility != models.VisibilityMembers {
		t.Fatal("rejected batch changed sharing", file, err)
	}
	resp, raw = h.apiJSON(http.MethodPost, "/api/v1/library/album", key, map[string]any{"files": []string{first, second}, "album": album.ID})
	if resp.StatusCode != 200 {
		t.Fatal("album API numeric id", resp.StatusCode, string(raw))
	}
	a, err := h.store.AlbumByID(t.Context(), album.ID)
	if err != nil || a.FileCount != 2 {
		t.Fatal("album action duplicated membership", a, err)
	}
	for _, path := range []string{"/gallery/album", "/gallery/visibility", "/trash/" + first + "/restore", "/trash/" + first + "/purge"} {
		if resp, _ := me.get(path); resp.StatusCode != 405 {
			t.Fatal("mutating route accepted GET", path, resp.StatusCode)
		}
		if resp := doForm(t, me.client, h.server.URL, path, url.Values{}); resp.StatusCode != 403 {
			t.Fatal("mutating route accepted missing CSRF", path, resp.StatusCode)
		}
	}
	for _, path := range []string{"/api/v1/library/visibility", "/api/v1/library/album"} {
		if resp, _ := h.apiJSON(http.MethodPost, path, "", nil); resp.StatusCode != 401 {
			t.Fatal("API missing auth", path, resp.StatusCode)
		}
	}
}

func TestGalleryCaptureDateFiltersAndAPIValidation(t *testing.T) {
	h := newLibraryHarness(t, nil)
	owner := h.seedUser("alice")
	me := h.sessionFor(t, owner.ID)
	key := h.seedKey(owner.ID, "test", nil)
	old, newer, unknown := h.uploadOne(key, "old.png", 32, 32), h.uploadOne(key, "new.png", 40, 40), h.uploadOne(key, "unknown.png", 48, 48)
	for id, taken := range map[string]string{old: "31 December 2019 at 23:59", newer: "1 January 2024 at 00:01"} {
		file, err := h.store.FileByID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.store.SetBlobDetails(t.Context(), file.SHA256, (&metadata.Details{Taken: taken}).Encode()); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query string
		want  []string
	}{{"sort=taken", []string{newer, old, unknown}}, {"sort=taken&year=2024&month=01", []string{newer}}, {"year=unknown", []string{unknown}}, {"year=2019", []string{old}}} {
		resp, page := me.get("/gallery?" + tc.query)
		if resp.StatusCode != 200 {
			t.Fatal("gallery filters", tc.query, resp.StatusCode)
		}
		last := -1
		for _, id := range tc.want {
			position := strings.Index(page, `id="file-`+id+`"`)
			if position <= last {
				t.Fatal("gallery date ordering", tc.query, id)
			}
			last = position
		}
		resp, raw := h.apiJSON(http.MethodGet, "/api/v1/files?"+tc.query, key, nil)
		var listing struct {
			Files []apiFileJSON
			Total int
		}
		if resp.StatusCode != 200 || json.Unmarshal(raw, &listing) != nil || listing.Total != len(tc.want) {
			t.Fatal("date API schema", resp.StatusCode, string(raw))
		}
		got := []string{}
		for _, file := range listing.Files {
			got = append(got, file.ID)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatal("date API result", got, tc.want)
		}
	}
	for _, query := range []string{"sort=random", "sort=taken%3BDROP+TABLE+files", "year=0000", "year=%2B123", "year=2024%27+OR+1=1--", "year=2024&month=13", "year=2024&month=1", "month=01", "year=unknown&month=01", "year=2024&month=%2B1"} {
		if resp, _ := me.get("/gallery?" + query); resp.StatusCode != 400 {
			t.Fatal("invalid date query", query, resp.StatusCode)
		}
		if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/files?"+query, key, nil); resp.StatusCode != 400 {
			t.Fatal("invalid API date query", query, resp.StatusCode)
		}
	}
}

func TestTrashCannotEvadePendingModeration(t *testing.T) {
	h := newLibraryHarness(t, nil)
	h.provisionAdmin("boss")
	owner, reporter, mod := h.seedUser("owner"), h.seedUser("reporter"), h.seedUser("mod")
	if err := h.store.SetUserRole(t.Context(), mod.ID, models.RoleModerator); err != nil {
		t.Fatal(err)
	}
	me, them, staff := h.sessionFor(t, owner.ID), h.sessionFor(t, reporter.ID), h.sessionFor(t, mod.ID)
	id := uploadAs(t, me, "reported.png", "public")
	if resp, _ := them.post("/reports", url.Values{"target_kind": {"file"}, "target_id": {id}, "reason": {"abuse"}}); resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
	}
	reportID := lastReportID(t, h)
	if resp, _ := me.post("/f/"+id+"/delete", url.Values{}); resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
	}
	if resp, page := staff.get("/moderation"); resp.StatusCode != 200 || !strings.Contains(page, "in its owner’s Trash") || strings.Contains(page, "no longer exists") {
		t.Fatal("reported Trash is not actionable", resp.StatusCode, page)
	}
	if resp, _ := staff.post("/moderation/reports/"+itoa64(reportID)+"/resolve", url.Values{"action": {"remove"}}); resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
	}
	if resp, _ := me.post("/trash/"+id+"/restore", url.Values{}); resp.StatusCode != 404 {
		t.Fatal("moderation removal restorable", resp.StatusCode)
	}
	if n, err := h.store.CountTrash(t.Context(), owner.ID); err != nil || n != 0 {
		t.Fatal("moderation retained Trash", n, err)
	}
}

func TestCaptureUploadAndTrashSurviveVerifiedBackupRestore(t *testing.T) {
	h := newLibraryHarness(t, func(cfg *config.Config) { cfg.SecretKeyFile = filepath.Join(cfg.DataDir, "secret.key") })
	owner := h.seedUser("alice")
	me := h.sessionFor(t, owner.ID)
	decoded, _, err := image.Decode(bytes.NewReader(pngFixture(t, 32, 24)))
	if err != nil {
		t.Fatal(err)
	}
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, decoded, nil); err != nil {
		t.Fatal(err)
	}
	source := exifwrite.Attach(jpegBytes.Bytes(), exifwrite.Tags{Taken: "2024:02:29 23:59:00"})
	resp, body := me.upload(map[string]string{"visibility": "private"}, []uploadFile{{name: "camera.jpg", data: source}})
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, body)
	}
	id := firstFileID(t, body)
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil || file.TakenAt != "2024-02-29T23:59:00" {
		t.Fatal("upload did not index camera date", file, err)
	}
	if resp, _ := me.post("/f/"+id+"/delete", url.Values{}); resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
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
	st := store.New(database)
	trashed, err := st.TrashedFile(t.Context(), id, owner.ID)
	if err != nil || trashed.TakenAt != file.TakenAt {
		t.Fatal("backup lost Trash or date", trashed, err)
	}
	if err := st.RestoreTrashedFile(t.Context(), id, owner.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	restoredFile, err := st.FileByID(t.Context(), id)
	if err != nil || restoredFile.SHA256 != file.SHA256 || restoredFile.Visibility != models.VisibilityPrivate {
		t.Fatal("restored backup lost original metadata", restoredFile, err)
	}
}

// These integration tests exercise the complete HTTP router, middleware and
// real storage without a listening socket. Ordinary clients retain their own
// cookie jars and redirect handling through an in-process request recorder.
func newLibraryHarness(t *testing.T, configure func(*config.Config)) *harness {
	t.Helper()
	h := newHarnessTransport(t, configure, mailOff, false)
	h.client.Transport = libraryRouterTransport{handler: h.srv.Handler()}
	return h
}

type libraryRouterTransport struct{ handler http.Handler }

func (transport libraryRouterTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.RequestURI = request.URL.RequestURI()
	request.RemoteAddr = "127.0.0.1:12345"
	if request.Body != nil {
		defer request.Body.Close()
	}
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	response.Request = request
	return response, nil
}
