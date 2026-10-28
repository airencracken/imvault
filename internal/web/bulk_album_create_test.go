// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

func TestBulkCreateAlbumWebAndAPI(t *testing.T) {
	h := newLibraryHarness(t, nil)
	owner := h.seedUser("alice")
	me := h.sessionFor(t, owner.ID)
	key := h.seedKey(owner.ID, "test", nil)
	first, second := h.uploadOne(key, "one.png", 32, 32), h.uploadOne(key, "two.png", 40, 40)
	resp, page := me.get("/gallery")
	if resp.StatusCode != 200 || !strings.Contains(page, `<option value="new">Create a new album</option>`) || !strings.Contains(page, `name="album_title"`) {
		t.Fatal("A gallery with no albums must offer inline album creation", resp.StatusCode)
	}
	form := url.Values{
		"csrf_token": {h.csrfFor(t, me.client)}, "files": {first, second, first}, "album": {"new"},
		"album_title": {"  <Picnic> & friends  "}, "q": {"one&two"}, "sort": {"taken"},
		"year": {"2024"}, "month": {"01"}, "page": {"2"}, "visibility": {"public"},
	}
	resp = doForm(t, me.client, h.server.URL, "/gallery/album", form)
	destination, err := resp.Location()
	if err != nil || resp.StatusCode != 303 || destination.Path != "/gallery" {
		t.Fatal("album creation redirect", resp.StatusCode, destination, err)
	}
	for _, field := range []string{"q", "sort", "year", "month", "page"} {
		if destination.Query().Get(field) != form.Get(field) {
			t.Fatal("album creation lost gallery filters", field, destination)
		}
	}
	albums, err := h.store.AlbumsByUser(t.Context(), owner.ID)
	if err != nil || len(albums) != 1 {
		t.Fatal("new album missing", albums, err)
	}
	album := albums[0]
	if album.Title != "<Picnic> & friends" || album.Visibility != models.VisibilityPrivate || album.Access != models.AlbumAccessOwner || album.FileCount != 2 {
		t.Fatalf("wrong album defaults or duplicate membership: %+v", album)
	}
	for _, id := range []string{first, second} {
		file, err := h.store.FileByID(t.Context(), id)
		if err != nil || file.Visibility != models.VisibilityMembers {
			t.Fatal("album action applied unrelated visibility selector", file, err)
		}
	}
	_, page = me.get("/gallery")
	if !strings.Contains(page, fmt.Sprintf(`value="%d"`, album.ID)) || !strings.Contains(page, "&lt;Picnic&gt; &amp; friends") {
		t.Fatal("new album must appear escaped in the normal picker")
	}
	resp, raw := h.apiJSON(http.MethodPost, "/api/v1/library/album", key, map[string]any{
		"files": []string{first, second, first}, "album": "new", "album_title": "API picnic",
	})
	var result map[string]int
	if resp.StatusCode != 200 || json.Unmarshal(raw, &result) != nil || len(result) != 1 || result["updated"] != 2 {
		t.Fatal("bulk creation changed the API response schema", resp.StatusCode, string(raw))
	}
	apiAlbum, err := h.store.AlbumBySlug(t.Context(), "api-picnic")
	if err != nil || apiAlbum.FileCount != 2 || apiAlbum.UserID != owner.ID || apiAlbum.Visibility != models.VisibilityPrivate {
		t.Fatal("API creation did not attach owned files privately", apiAlbum, err)
	}
}

func TestBulkCreateAlbumRejectsUnauthorizedAndMalformedRequests(t *testing.T) {
	h := newLibraryHarness(t, nil)
	owner, other := h.seedUser("alice"), h.seedUser("bob")
	me := h.sessionFor(t, owner.ID)
	key, otherKey := h.seedKey(owner.ID, "test", nil), h.seedKey(other.ID, "test", nil)
	first, foreign := h.uploadOne(key, "own.png", 32, 32), h.uploadOne(otherKey, "other.png", 40, 40)
	for _, tc := range []struct {
		files  any
		title  any
		status int
	}{
		{[]string{first, foreign}, "Rejected", 404},
		{[]string{first, "missing"}, "Rejected", 404},
		{[]string{}, "Rejected", 400},
		{[]any{first, 42}, "Rejected", 400},
		{[]string{first}, "", 400},
		{[]string{first}, "  ", 400},
		{[]string{first}, strings.Repeat("界", 121), 400},
		{[]string{first}, "bad\x00title", 400},
		{[]string{first}, true, 400},
		{[]string{first}, []string{"bad"}, 400},
		{[]string{first}, nil, 400},
	} {
		resp, raw := h.apiJSON(http.MethodPost, "/api/v1/library/album", key, map[string]any{
			"files": tc.files, "album": "new", "album_title": tc.title,
		})
		if resp.StatusCode != tc.status {
			t.Fatalf("invalid new album: status=%d want=%d body=%s", resp.StatusCode, tc.status, raw)
		}
	}
	form := url.Values{"files": {first}, "album": {"new"}, "album_title": {"CSRF attempt"}}
	if resp := doForm(t, me.client, h.server.URL, "/gallery/album", form); resp.StatusCode != 403 {
		t.Fatal("new album action accepted missing CSRF", resp.StatusCode)
	}
	form.Set("csrf_token", h.csrfFor(t, me.client))
	form.Set("album_title", "  ")
	resp := doForm(t, me.client, h.server.URL, "/gallery/album", form)
	destination, err := resp.Location()
	if resp.StatusCode != 303 || err != nil {
		t.Fatal("plain form validation must return to the gallery", resp.StatusCode, err)
	}
	if resp, page := me.get(destination.RequestURI()); resp.StatusCode != 200 || !strings.Contains(page, "album title") {
		t.Fatal("plain form validation must explain the missing title", resp.StatusCode)
	}
	if resp, _ := h.apiJSON(http.MethodPost, "/api/v1/library/album", "", form); resp.StatusCode != 401 {
		t.Fatal("new album API accepted missing authentication", resp.StatusCode)
	}
	for _, path := range []string{"/gallery/album", "/api/v1/library/album"} {
		if resp, _ := me.get(path); resp.StatusCode != 405 {
			t.Fatal("new album action accepted GET", path, resp.StatusCode)
		}
	}
	albums, err := h.store.AlbumsByUser(t.Context(), owner.ID)
	if err != nil || len(albums) != 0 {
		t.Fatal("rejected request left an empty album", albums, err)
	}
}
