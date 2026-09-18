// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// apiJSON sends a JSON payload, or no body at all when payload is nil.
func (h *harness) apiJSON(method, path, key string, payload any) (*http.Response, []byte) {
	h.t.Helper()

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			h.t.Fatalf("encode payload: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	return h.apiDo(method, path, key, body, "application/json")
}

// uploadOne uploads a single image and returns its id.
func (h *harness) uploadOne(key, name string, w, height int) string {
	h.t.Helper()

	_, raw := h.apiUpload(key, nil, map[string][]byte{name: pngFixture(h.t, w, height)})
	up := decodeUpload(h.t, raw)
	if len(up.Files) == 0 {
		h.t.Fatalf("upload %s failed: %s", name, truncate(string(raw)))
	}
	return up.Files[0].ID
}

func decodeAlbum(t *testing.T, raw []byte) apiAlbumJSON {
	t.Helper()

	var album apiAlbumJSON
	if err := json.Unmarshal(raw, &album); err != nil {
		t.Fatalf("decode album: %v (body: %s)", err, truncate(string(raw)))
	}
	return album
}

func decodeTags(t *testing.T, raw []byte) []apiTagJSON {
	t.Helper()

	var out apiTagsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode tags: %v (body: %s)", err, truncate(string(raw)))
	}
	return out.Tags
}

func hasTagNamed(tags []apiTagJSON, name string) bool {
	for _, tag := range tags {
		if tag.Name == name {
			return true
		}
	}
	return false
}

func TestAPIAlbumLifecycle(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	// Create.
	resp, raw := h.apiJSON(http.MethodPost, "/api/v1/albums", key, map[string]any{
		"title":       "Summer 2026",
		"description": "Warm ones",
		"public":      true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create album = %d, want 201 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}
	album := decodeAlbum(t, raw)
	if album.ID == 0 || album.Slug != "summer-2026" {
		t.Fatalf("album = %+v, want an id and slug summer-2026", album)
	}
	if !album.Public || album.FileCount != 0 {
		t.Errorf("album = %+v, want public with no files", album)
	}
	if !strings.HasSuffix(album.PageURL, "/a/summer-2026") {
		t.Errorf("page_url = %q", album.PageURL)
	}

	// List.
	resp, raw = h.apiJSON(http.MethodGet, "/api/v1/albums", key, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list albums = %d, want 200", resp.StatusCode)
	}
	var list struct {
		Albums []apiAlbumJSON `json:"albums"`
		Total  int            `json:"total"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 || len(list.Albums) != 1 {
		t.Errorf("list = %d albums, want 1", len(list.Albums))
	}

	// Add files by slug.
	first := h.uploadOne(key, "one.png", 40, 40)
	second := h.uploadOne(key, "two.png", 40, 40)

	resp, raw = h.apiJSON(http.MethodPost, "/api/v1/albums/summer-2026/files", key, map[string]any{
		"files": []string{first, second},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add files = %d, want 200 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}
	var added struct {
		Album apiAlbumJSON `json:"album"`
		Added int          `json:"added"`
	}
	if err := json.Unmarshal(raw, &added); err != nil {
		t.Fatalf("decode add: %v", err)
	}
	if added.Added != 2 || added.Album.FileCount != 2 {
		t.Errorf("added = %d, album file_count = %d, want 2 and 2", added.Added, added.Album.FileCount)
	}

	// Read back by numeric id this time, which must also work.
	resp, raw = h.apiJSON(http.MethodGet, "/api/v1/albums/"+strconv.FormatInt(album.ID, 10), key, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get album = %d, want 200", resp.StatusCode)
	}
	var detail apiAlbumDetailJSON
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("decode album detail: %v", err)
	}
	if len(detail.Files) != 2 {
		t.Fatalf("album has %d files, want 2", len(detail.Files))
	}
	if detail.Files[0].ID != first && detail.Files[1].ID != first {
		t.Error("the album does not contain the first file")
	}

	// A partial update must leave the other fields alone.
	resp, raw = h.apiJSON(http.MethodPatch, "/api/v1/albums/summer-2026", key, map[string]any{
		"public": false,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch album = %d, want 200 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}
	patched := decodeAlbum(t, raw)
	if patched.Public {
		t.Error("public was not turned off")
	}
	if patched.Title != "Summer 2026" || patched.Description != "Warm ones" {
		t.Errorf("patch clobbered fields: %+v", patched)
	}

	// Remove one member.
	resp, raw = h.apiJSON(http.MethodDelete, "/api/v1/albums/summer-2026/files/"+first, key, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove file = %d, want 200", resp.StatusCode)
	}
	if after := decodeAlbum(t, raw); after.FileCount != 1 {
		t.Errorf("file_count = %d, want 1", after.FileCount)
	}

	// Deleting the album must not delete its files.
	resp, _ = h.apiJSON(http.MethodDelete, "/api/v1/albums/summer-2026", key, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete album = %d, want 200", resp.StatusCode)
	}
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/files/"+second, key, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("deleting the album removed its files (status %d)", resp.StatusCode)
	}
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/albums/summer-2026", key, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("album still readable after delete (status %d)", resp.StatusCode)
	}
}

func TestAPIAlbumAcceptsFormEncoding(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	form := url.Values{"title": {"Form Album"}, "public": {"1"}}
	resp, raw := h.apiDo(http.MethodPost, "/api/v1/albums", key,
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("form create = %d, want 201 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}
	album := decodeAlbum(t, raw)
	if album.Title != "Form Album" || !album.Public {
		t.Errorf("album = %+v, want title 'Form Album' and public", album)
	}
}

func TestAPIAlbumRequiresATitle(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	resp, raw := h.apiJSON(http.MethodPost, "/api/v1/albums", key, map[string]any{"description": "no title"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}

	// A malformed JSON body is a client error, not a 500.
	resp, _ = h.apiDo(http.MethodPost, "/api/v1/albums", key, strings.NewReader("{not json"), "application/json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed JSON status = %d, want 400", resp.StatusCode)
	}
}

func TestAPIAlbumsAreIsolatedBetweenAccounts(t *testing.T) {
	h := newHarness(t)

	alice := h.seedUser("alice")
	bob := h.seedUser("bob")
	aliceKey := h.seedKey(alice.ID, "alice", nil)
	bobKey := h.seedKey(bob.ID, "bob", nil)

	_, raw := h.apiJSON(http.MethodPost, "/api/v1/albums", aliceKey, map[string]any{
		"title": "Private Album",
	})
	album := decodeAlbum(t, raw)

	// Bob must not see it, read it, change it or delete it.
	if _, body := h.apiJSON(http.MethodGet, "/api/v1/albums", bobKey, nil); strings.Contains(string(body), "Private Album") {
		t.Error("bob's album list leaks alice's album")
	}
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/albums/private-album"},
		{http.MethodGet, "/api/v1/albums/" + strconv.FormatInt(album.ID, 10)},
		{http.MethodPatch, "/api/v1/albums/private-album"},
		{http.MethodDelete, "/api/v1/albums/private-album"},
		{http.MethodPost, "/api/v1/albums/private-album/files"},
	} {
		resp, _ := h.apiJSON(tc.method, tc.path, bobKey, map[string]any{"files": []string{"x"}})
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s as bob = %d, want 404", tc.method, tc.path, resp.StatusCode)
		}
	}

	// Alice still has it.
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/albums/private-album", aliceKey, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("alice lost access to her album (%d)", resp.StatusCode)
	}
}

func TestAPIAlbumIgnoresFilesTheCallerDoesNotOwn(t *testing.T) {
	h := newHarness(t)

	alice := h.seedUser("alice")
	bob := h.seedUser("bob")
	aliceKey := h.seedKey(alice.ID, "alice", nil)
	bobKey := h.seedKey(bob.ID, "bob", nil)

	bobFile := h.uploadOne(bobKey, "bobs.png", 30, 30)
	aliceFile := h.uploadOne(aliceKey, "alices.png", 30, 30)

	_, raw := h.apiJSON(http.MethodPost, "/api/v1/albums", aliceKey, map[string]any{"title": "Mine"})
	album := decodeAlbum(t, raw)

	resp, raw := h.apiJSON(http.MethodPost, "/api/v1/albums/"+album.Slug+"/files", aliceKey, map[string]any{
		"files": []string{bobFile, aliceFile, "doesnotexist00"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add files = %d, want 200", resp.StatusCode)
	}

	var added struct {
		Added int `json:"added"`
	}
	if err := json.Unmarshal(raw, &added); err != nil {
		t.Fatal(err)
	}
	if added.Added != 1 {
		t.Errorf("added = %d, want only the caller's own file", added.Added)
	}

	_, raw = h.apiJSON(http.MethodGet, "/api/v1/albums/"+album.Slug, aliceKey, nil)
	var detail apiAlbumDetailJSON
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	for _, f := range detail.Files {
		if f.ID == bobFile {
			t.Error("bob's file ended up in alice's album")
		}
	}
}

func TestAPIFileTags(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	id := h.uploadOne(key, "tagged.png", 40, 40)

	// A new upload reports an empty tag array, not null.
	_, raw := h.apiJSON(http.MethodGet, "/api/v1/files/"+id, key, nil)
	var file apiFileJSON
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if file.Tags == nil {
		t.Error("tags should be an empty array rather than null")
	}

	// Add.
	resp, raw := h.apiJSON(http.MethodPost, "/api/v1/files/"+id+"/tags", key, map[string]any{
		"name": "Holiday Snaps",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add tag = %d, want 200 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}
	tags := decodeTags(t, raw)
	if len(tags) != 1 || tags[0].Name != "Holiday Snaps" || tags[0].Slug != "holiday-snaps" {
		t.Fatalf("tags = %+v, want Holiday Snaps with slug holiday-snaps", tags)
	}
	tagID := tags[0].ID

	// The tag now shows up on the file and in the global index.
	_, raw = h.apiJSON(http.MethodGet, "/api/v1/files/"+id, key, nil)
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if !hasTagNamed(file.Tags, "Holiday Snaps") {
		t.Error("file metadata does not include the new tag")
	}
	_, raw = h.apiJSON(http.MethodGet, "/api/v1/tags", key, nil)
	if !hasTagNamed(decodeTags(t, raw), "Holiday Snaps") {
		t.Error("tag index does not include the new tag")
	}

	// Remove by slug, then re-add and remove by numeric id: all three
	// reference forms must work.
	if resp, _ := h.apiJSON(http.MethodDelete, "/api/v1/files/"+id+"/tags/holiday-snaps", key, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("remove by slug = %d, want 200", resp.StatusCode)
	}
	if _, raw = h.apiJSON(http.MethodGet, "/api/v1/files/"+id, key, nil); !json.Valid(raw) {
		t.Fatal("invalid json")
	}
	_ = json.Unmarshal(raw, &file)
	if len(file.Tags) != 0 {
		t.Errorf("tag survived removal by slug: %+v", file.Tags)
	}

	h.apiJSON(http.MethodPost, "/api/v1/files/"+id+"/tags", key, map[string]any{"name": "Second"})
	_, raw = h.apiJSON(http.MethodDelete, "/api/v1/files/"+id+"/tags/"+strconv.FormatInt(tagID, 10), key, nil)
	_ = json.Unmarshal(raw, &file)
	if len(file.Tags) != 0 {
		t.Errorf("remove by id failed: %+v", file.Tags)
	}

	// An unknown tag reference is a 404, not a silent success.
	if resp, _ := h.apiJSON(http.MethodDelete, "/api/v1/files/"+id+"/tags/nosuchtag", key, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown tag status = %d, want 404", resp.StatusCode)
	}

	// Adding without a name is a client error.
	if resp, _ := h.apiJSON(http.MethodPost, "/api/v1/files/"+id+"/tags", key, map[string]any{}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("nameless tag status = %d, want 400", resp.StatusCode)
	}
}

func TestAPITagsRequireOwningTheFile(t *testing.T) {
	h := newHarness(t)

	alice := h.seedUser("alice")
	bob := h.seedUser("bob")
	aliceKey := h.seedKey(alice.ID, "alice", nil)
	bobKey := h.seedKey(bob.ID, "bob", nil)

	id := h.uploadOne(aliceKey, "alices.png", 30, 30)

	if resp, _ := h.apiJSON(http.MethodPost, "/api/v1/files/"+id+"/tags", bobKey, map[string]any{"name": "mine"}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("bob tagging alice's file = %d, want 404", resp.StatusCode)
	}
	if resp, _ := h.apiJSON(http.MethodDelete, "/api/v1/files/"+id+"/tags/mine", bobKey, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("bob untagging alice's file = %d, want 404", resp.StatusCode)
	}
}

func TestAPIPatchFileVisibility(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	id := h.uploadOne(key, "toggle.png", 30, 30)

	// Private by default, so an anonymous viewer cannot reach it.
	anon := &http.Client{}
	if resp, err := anon.Get(h.server.URL + "/f/" + id); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("private file visible to anonymous: %d", resp.StatusCode)
		}
	}

	resp, raw := h.apiJSON(http.MethodPatch, "/api/v1/files/"+id, key, map[string]any{"public": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch = %d, want 200 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}
	var file apiFileJSON
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if !file.Public {
		t.Error("public was not enabled")
	}

	if resp, err := anon.Get(h.server.URL + "/f/" + id); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("public file not reachable: %d", resp.StatusCode)
		}
	}

	// Turning it back off works too.
	if resp, _ := h.apiJSON(http.MethodPatch, "/api/v1/files/"+id, key, map[string]any{"public": false}); resp.StatusCode != http.StatusOK {
		t.Error("could not turn visibility back off")
	}

	// A patch with nothing actionable is a client error.
	if resp, _ := h.apiJSON(http.MethodPatch, "/api/v1/files/"+id, key, map[string]any{}); resp.StatusCode != http.StatusBadRequest {
		t.Error("empty patch should be rejected")
	}
}

func TestAPIListFilters(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	still := h.uploadOne(key, "still.png", 30, 30)

	// An animated GIF, made public.
	_, raw := h.apiUpload(key, map[string]string{"public": "1"}, map[string][]byte{
		"wiggle.gif": animatedGIF(t, 4, 24, 24),
	})
	animatedUpload := decodeUpload(t, raw)
	if len(animatedUpload.Files) != 1 {
		t.Fatalf("gif upload failed: %s", truncate(string(raw)))
	}

	// Tag the still and put it in an album.
	h.apiJSON(http.MethodPost, "/api/v1/files/"+still+"/tags", key, map[string]any{"name": "keep"})
	_, raw = h.apiJSON(http.MethodPost, "/api/v1/albums", key, map[string]any{"title": "Filter Album"})
	album := decodeAlbum(t, raw)
	h.apiJSON(http.MethodPost, "/api/v1/albums/"+album.Slug+"/files", key, map[string]any{
		"files": []string{still},
	})

	countFiles := func(query string) int {
		t.Helper()
		resp, raw := h.apiJSON(http.MethodGet, "/api/v1/files"+query, key, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d (body: %s)", query, resp.StatusCode, truncate(string(raw)))
		}
		var out struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out.Total
	}

	if got := countFiles(""); got != 2 {
		t.Errorf("unfiltered total = %d, want 2", got)
	}
	if got := countFiles("?kind=image"); got != 1 {
		t.Errorf("kind=image total = %d, want 1", got)
	}
	if got := countFiles("?kind=animated"); got != 1 {
		t.Errorf("kind=animated total = %d, want 1", got)
	}
	if got := countFiles("?kind=video"); got != 0 {
		t.Errorf("kind=video total = %d, want 0", got)
	}
	if got := countFiles("?public=1"); got != 1 {
		t.Errorf("public=1 total = %d, want 1", got)
	}
	if got := countFiles("?public=0"); got != 1 {
		t.Errorf("public=0 total = %d, want 1", got)
	}
	if got := countFiles("?tag=keep"); got != 1 {
		t.Errorf("tag=keep total = %d, want 1", got)
	}
	if got := countFiles("?album=filter-album"); got != 1 {
		t.Errorf("album filter total = %d, want 1", got)
	}
	if got := countFiles("?q=still"); got != 1 {
		t.Errorf("search total = %d, want 1", got)
	}

	// Bad filters are rejected rather than silently ignored.
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/files?kind=nonsense", key, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown kind status = %d, want 400", resp.StatusCode)
	}
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/files?tag=nosuchtag", key, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown tag filter status = %d, want 404", resp.StatusCode)
	}
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/files?album=nosuchalbum", key, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown album filter status = %d, want 404", resp.StatusCode)
	}
}
