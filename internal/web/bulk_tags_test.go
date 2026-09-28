// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/store"
)

func TestBulkTagsAPIContractAndGalleryForm(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	key := h.seedKey(u.ID, "test", nil)
	first, second := h.uploadOne(key, "one.png", 32, 32), h.uploadOne(key, "two.png", 32, 32)
	owner := h.sessionFor(t, u.ID)
	resp, page := owner.get("/gallery")
	if resp.StatusCode != 200 || strings.Count(page, `form="bulk-tags"`) != 2 || !strings.Contains(page, `action="/gallery/tags"`) {
		t.Fatalf("gallery selection form missing: %d", resp.StatusCode)
	}
	resp, raw := h.apiJSON(http.MethodPost, "/api/v1/files/tags", key, map[string]any{"files": []string{first, second, first}, "tags": []string{"Holiday", "Family"}})
	var result map[string]int
	if resp.StatusCode != 200 || json.Unmarshal(raw, &result) != nil || result["tagged"] != 2 || len(result) != 1 {
		t.Fatalf("bulk response schema: %d %s", resp.StatusCode, raw)
	}
	resp = doForm(t, owner.client, h.server.URL, "/gallery/tags", url.Values{
		"csrf_token": {h.csrfFor(t, owner.client)}, "files": {first, second}, "tags": {"Together, Family"}, "q": {"one&two"}, "page": {"2"},
	})
	location, err := resp.Location()
	if err != nil || resp.StatusCode != 303 || location.Path != "/gallery" || location.Query().Get("q") != "one&two" || location.Query().Get("page") != "2" || location.Query().Get("notice") != "Tags added to 2 files." {
		t.Fatalf("form redirect: %v, %v", location, err)
	}
	for _, id := range []string{first, second} {
		_, raw = h.apiJSON(http.MethodGet, "/api/v1/files/"+id, key, nil)
		var file apiFileJSON
		if err := json.Unmarshal(raw, &file); err != nil || len(file.Tags) != 3 {
			t.Fatalf("saved tags: %s (%v)", raw, err)
		}
	}
	resp = doForm(t, owner.client, h.server.URL, "/gallery/tags", url.Values{"files": {first}, "tags": {"forbidden"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal("bulk form accepted missing CSRF")
	}
}

func TestBulkTagsAPIRejectsInvalidBatchesWithoutChanges(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	other := h.seedUser("bob")
	key, otherKey := h.seedKey(u.ID, "test", nil), h.seedKey(other.ID, "test", nil)
	own, foreign := h.uploadOne(key, "own.png", 32, 32), h.uploadOne(otherKey, "foreign.png", 32, 32)
	for _, tc := range []struct {
		body   any
		status int
	}{
		{map[string]any{"files": []string{own, foreign}, "tags": []string{"new"}}, 404},
		{map[string]any{"files": []string{own, "missing"}, "tags": []string{"new"}}, 404},
		{map[string]any{"files": []string{own}, "tags": []string{"new", ""}}, 400},
		{map[string]any{"files": []string{own}, "tags": []any{"new", 42}}, 400},
		{map[string]any{"files": []string{own}, "tags": []string{strings.Repeat("x", 49)}}, 400},
		{map[string]any{"files": []string{own}, "tags": []string{"new\x00tag"}}, 400},
		{map[string]any{"files": make([]string, store.MaxBulkTagFiles+1), "tags": []string{"new"}}, 400},
		{map[string]any{"files": []string{own}, "tags": make([]string, store.MaxBulkTags+1)}, 400},
		{map[string]any{"files": 42, "tags": []string{"new"}}, 400},
		{nil, 400}, {[]string{"not an object"}, 400},
	} {
		resp, raw := h.apiJSON(http.MethodPost, "/api/v1/files/tags", key, tc.body)
		if resp.StatusCode != tc.status {
			t.Errorf("body %v: %d %s", tc.body, resp.StatusCode, raw)
		}
		var message map[string]json.RawMessage
		if json.Unmarshal(raw, &message) != nil || message["error"] == nil {
			t.Fatalf("error response schema: %s", raw)
		}
	}
	for _, id := range []string{own, foreign} {
		tags, err := h.store.TagsForFile(t.Context(), id)
		if err != nil || len(tags) != 0 {
			t.Fatal("rejected batch changed tags")
		}
	}
	if resp, _ := h.apiJSON(http.MethodPost, "/api/v1/files/tags", "", nil); resp.StatusCode != 401 {
		t.Fatal("bulk API does not require authentication")
	}
	if resp, _ := h.newSession(t).get("/gallery/tags"); resp.StatusCode != 405 {
		t.Fatalf("bulk route accepted GET: %d", resp.StatusCode)
	}
}

func TestBulkTagsEscapesNamesAndAcceptsAPIForm(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	key := h.seedKey(u.ID, "test", nil)
	id := h.uploadOne(key, "photo.png", 32, 32)
	name := "<script>alert(1)</script>"
	form := url.Values{"files": {id}, "tags": {name, "Family"}}
	resp, raw := h.apiDo(http.MethodPost, "/api/v1/files/tags", key, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if resp.StatusCode != 200 {
		t.Fatalf("form API: %d %s", resp.StatusCode, raw)
	}
	_, page := h.sessionFor(t, u.ID).get("/f/" + id)
	if strings.Contains(page, name) || !strings.Contains(page, "&lt;script&gt;") {
		t.Fatal("tag name is not rendered as escaped text")
	}
}
