// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

func TestOwnersCanRenameWithoutChangingLinksOrSharedContent(t *testing.T) {
	h := newHarness(t)
	alice, bob := h.seedUser("alice"), h.seedUser("bob")
	owner := h.sessionFor(t, alice.ID)
	id := uploadAs(t, owner, "camera.png", "private")
	duplicate := uploadAs(t, h.sessionFor(t, bob.ID), "copy.png", "private")
	before, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	original := storedOriginal(t, h, id)
	path := "/f/" + id
	beforeDownload, _ := owner.get(path + "/raw")
	oldETag := beforeDownload.Header.Get("ETag")
	_, page := owner.get(path)
	if !strings.Contains(page, `action="`+path+`/rename"`) {
		t.Fatal("owner has no rename form")
	}
	resp, _ := owner.post(path+"/rename", url.Values{"name": {"  Family picnic.png  "}})
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), path+"?") {
		t.Fatalf("rename = %d, location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	after, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if after.OriginalName != "Family picnic.png" || after.SHA256 != before.SHA256 || after.ObjectKey != before.ObjectKey || after.Visibility != before.Visibility {
		t.Fatal("rename changed more than the filename or failed to persist")
	}
	other, err := h.store.FileByID(t.Context(), duplicate)
	if err != nil || other.SHA256 != before.SHA256 || other.OriginalName != "copy.png" {
		t.Fatal("renaming shared content changed somebody else's filename")
	}
	if !bytes.Equal(original, storedOriginal(t, h, id)) {
		t.Fatal("rename changed the original bytes")
	}
	_, page = owner.get(path)
	if !strings.Contains(page, `<h1 class="filename">Family picnic.png</h1>`) {
		t.Fatal("photo page did not show the new name")
	}
	_, page = owner.get("/gallery?q=picnic")
	if !strings.Contains(page, `id="file-`+id+`"`) {
		t.Fatal("renamed file is not searchable")
	}
	resp, _ = owner.get(path + "/raw")
	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if err != nil || params["filename"] != "Family picnic.png" {
		t.Fatal("download still uses the old filename")
	}
	if resp.Header.Get("ETag") == oldETag || !strings.Contains(resp.Header.Get("Cache-Control"), "no-cache") {
		t.Fatal("download caching keeps the old filename")
	}
	req, err := http.NewRequest(http.MethodGet, h.server.URL+path+"/raw", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", oldETag)
	conditional, err := owner.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	conditional.Body.Close()
	if conditional.StatusCode != http.StatusOK {
		t.Fatal("an old download validator preserved the old filename")
	}
	key := h.seedKey(alice.ID, "test", nil)
	_, raw := h.apiJSON(http.MethodGet, "/api/v1/files/"+id, key, nil)
	var file apiFileJSON
	if err := json.Unmarshal(raw, &file); err != nil || file.Name != "Family picnic.png" {
		t.Fatal("API did not show the new filename")
	}
}

func TestRenameRequiresOwnerOrAdminAndCSRF(t *testing.T) {
	h := newHarness(t)
	alice, bob, mod := h.seedUser("alice"), h.seedUser("bob"), h.seedUser("moderator")
	if err := h.store.SetUserRole(t.Context(), mod.ID, models.RoleModerator); err != nil {
		t.Fatal(err)
	}
	owner := h.sessionFor(t, alice.ID)
	id := uploadAs(t, owner, "original.png", "public")
	path := "/f/" + id + "/rename"
	for _, user := range []int64{bob.ID, mod.ID} {
		viewer := h.sessionFor(t, user)
		if resp, _ := viewer.post(path, url.Values{"name": {"stolen.png"}, "user_id": {itoa64(alice.ID)}}); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("non-owner rename = %d", resp.StatusCode)
		}
		_, page := viewer.get("/f/" + id)
		if strings.Contains(page, `action="`+path+`"`) {
			t.Fatal("non-owner has a rename form")
		}
	}
	if resp, _ := h.newSession(t).post(path, url.Values{"name": {"guest.png"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("guest rename = %d", resp.StatusCode)
	}
	resp, err := owner.client.PostForm(h.server.URL+path, url.Values{"name": {"no-csrf.png"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal("rename accepted missing CSRF")
	}
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil || file.OriginalName != "original.png" {
		t.Fatal("rejected rename changed the file")
	}
	h.provisionAdmin("boss")
	if resp, _ := h.postForm(path, url.Values{"name": {"admin.png"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal("administrator could not rename")
	}
	if resp, _ := owner.post("/f/missing/rename", url.Values{"name": {"missing.png"}}); resp.StatusCode != http.StatusNotFound {
		t.Fatal("missing file accepted a rename")
	}
}

func TestRenameValidatesNamesAndEscapesHTML(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	owner := h.sessionFor(t, user.ID)
	id := uploadAs(t, owner, "original.png", "private")
	path := "/f/" + id
	for _, name := range []string{"", "  ", ".", "..", "../file.png", `C:\file.png`, "a\nb", "a\x00b", strings.Repeat("é", 201), string([]byte{0xff})} {
		if resp, _ := owner.post(path+"/rename", url.Values{"name": {name}}); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid name %q = %d", name, resp.StatusCode)
		}
	}
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil || file.OriginalName != "original.png" {
		t.Fatal("invalid input changed the name")
	}
	for _, name := range []string{strings.Repeat("é", 200), `<img src=x onerror=alert(1)>.png`} {
		if resp, _ := owner.post(path+"/rename", url.Values{"name": {name}}); resp.StatusCode != http.StatusSeeOther {
			t.Errorf("valid name rejected: %d", resp.StatusCode)
		}
	}
	_, page := owner.get(path)
	if strings.Contains(page, `<img src=x onerror=`) || !strings.Contains(page, "&lt;img src=x onerror=") {
		t.Fatal("filename was not escaped in HTML")
	}
}

func TestGalleryAndFavoritesSearchAttachedTags(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	owner := h.sessionFor(t, user.ID)
	id := uploadAs(t, owner, "camera.png", "private")
	if _, err := h.store.AddTag(t.Context(), id, &user.ID, "Camping"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetFavorite(t.Context(), user.ID, id, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/gallery?q=camping", "/favorites?q=camping"} {
		resp, page := owner.get(path)
		if resp.StatusCode != http.StatusOK || !strings.Contains(page, `id="file-`+id+`"`) {
			t.Fatalf("tag search failed at %s", path)
		}
	}
}
