// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

func TestFileDescriptionsArePlainTextAndBelongToEachUpload(t *testing.T) {
	h := newHarness(t)
	alice, bob := h.seedUser("alice"), h.seedUser("bob")
	owner, viewer := h.sessionFor(t, alice.ID), h.sessionFor(t, bob.ID)
	id := uploadAs(t, owner, "photo.png", "private")
	path := "/f/" + id
	text := "Our *summer* trip\n<script>alert(1)</script> & friends"
	resp, _ := owner.post(path+"/description", url.Values{"description": {"  " + strings.ReplaceAll(text, "\n", "\r\n") + "  "}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save description = %d", resp.StatusCode)
	}
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil || file.Description != text {
		t.Fatalf("stored description: %v (%v)", file, err)
	}
	_, page := owner.get(path)
	if !strings.Contains(page, `<p class="file-description">`+html.EscapeString(text)+`</p>`) || strings.Contains(page, "<script>alert") {
		t.Fatal("description was not rendered as escaped plain text")
	}
	if resp, page := viewer.get(path); resp.StatusCode != http.StatusNotFound || strings.Contains(page, "summer") {
		t.Fatal("description escaped private file visibility")
	}
	duplicate := uploadAs(t, viewer, "same-bytes.png", "public")
	copy, err := h.store.FileByID(t.Context(), duplicate)
	if err != nil || copy.SHA256 != file.SHA256 || copy.Description != "" {
		t.Fatal("a duplicate upload inherited somebody else's description")
	}
	_, entries, _ := readExport(t, owner)
	var manifest exportManifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil || len(manifest.Files) != 1 || manifest.Files[0].Description != text {
		t.Fatal("account export omitted the description")
	}
	if err := h.store.SetFileVisibility(t.Context(), id, models.VisibilityPublic); err != nil {
		t.Fatal(err)
	}
	_, page = h.newSession(t).get(path)
	if !strings.Contains(page, html.EscapeString(text)) || strings.Contains(page, `action="`+path+`/description"`) {
		t.Fatal("public viewer cannot read the description or was offered an editor")
	}
	if resp, _ := owner.post(path+"/description", url.Values{"description": {""}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal("could not clear the description")
	}
	_, page = owner.get(path)
	if strings.Contains(page, `<p class="file-description">`) || !strings.Contains(page, "Add description") {
		t.Fatal("cleared description still appears")
	}
}

func TestDescriptionsRequireOwnerOrAdminCSRFAndValidText(t *testing.T) {
	h := newHarness(t)
	alice, other := h.seedUser("alice"), h.seedUser("other")
	owner, viewer := h.sessionFor(t, alice.ID), h.sessionFor(t, other.ID)
	id := uploadAs(t, owner, "photo.png", "public")
	path := "/f/" + id + "/description"
	if resp, _ := owner.post(path, url.Values{"description": {"Original description"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal("could not set initial description")
	}
	for _, role := range []models.Role{models.RoleMember, models.RoleModerator} {
		if err := h.store.SetUserRole(t.Context(), other.ID, role); err != nil {
			t.Fatal(err)
		}
		if resp, _ := viewer.post(path, url.Values{"description": {"not yours"}}); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s edited somebody else's description", role)
		}
	}
	if resp, _ := h.newSession(t).post(path, url.Values{"description": {"guest"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal("guest request did not require sign-in")
	}
	resp, err := owner.client.PostForm(h.server.URL+path, url.Values{"description": {"missing csrf"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal("description edit accepted missing CSRF")
	}
	if resp, _ := owner.post(path, url.Values{}); resp.StatusCode != http.StatusBadRequest {
		t.Fatal("missing field silently cleared a description")
	}
	for _, text := range []string{strings.Repeat("a", 1001), strings.Repeat("é", 1001), "a\x00b", "a\x1bb", string([]byte{0xff})} {
		if resp, _ := owner.post(path, url.Values{"description": {text}}); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid text accepted: %d", resp.StatusCode)
		}
	}
	if file, err := h.store.FileByID(t.Context(), id); err != nil || file.Description != "Original description" {
		t.Fatal("rejected edit changed the saved description")
	}
	text := strings.Repeat("é", 1000)
	if resp, _ := owner.post(path, url.Values{"description": {text}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal("1,000 Unicode characters rejected")
	}
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil || file.Description != text {
		t.Fatal("boundary description was truncated")
	}
	h.provisionAdmin("boss")
	if resp, _ := h.postForm(path, url.Values{"description": {"admin edit"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal("administrator could not edit description")
	}
	if resp, _ := owner.post("/f/missing/description", url.Values{"description": {"missing"}}); resp.StatusCode != http.StatusNotFound {
		t.Fatal("missing file accepted a description")
	}
}

func TestVideoClipsHaveDescriptions(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	owner := h.sessionFor(t, user.ID)
	resp, body := owner.upload(map[string]string{"visibility": "private"}, []uploadFile{{name: "clip.webm", data: makeWebM(t)}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clip upload: %d, %s", resp.StatusCode, body)
	}
	id := firstFileID(t, body)
	if resp, _ := owner.post("/f/"+id+"/description", url.Values{"description": {"The birthday toast."}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal("clip description rejected")
	}
	_, page := owner.get("/f/" + id)
	if !strings.Contains(page, "<video") || !strings.Contains(page, `<p class="file-description">The birthday toast.</p>`) {
		t.Fatal("clip page lost its description or player")
	}
}

func TestAPIDescriptionsSupportPartialUpdatesAndRejectInvalidTypes(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	id := uploadAs(t, h.sessionFor(t, user.ID), "photo.png", "private")
	key := h.seedKey(user.ID, "test", nil)
	path := "/api/v1/files/" + id
	for _, value := range []any{nil, true, 123, []string{"no"}, strings.Repeat("x", 1001)} {
		resp, _ := h.apiJSON(http.MethodPatch, path, key, map[string]any{"description": value, "visibility": "public"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid description type/length accepted: %d", resp.StatusCode)
		}
		file, err := h.store.FileByID(t.Context(), id)
		if err != nil || file.Visibility != models.VisibilityPrivate || file.Description != "" {
			t.Fatal("invalid combined patch partially changed the file")
		}
	}
	resp, raw := h.apiJSON(http.MethodPatch, path, key, map[string]any{"description": "A day out.\nWith friends.", "visibility": "members", "metadata": "hidden"})
	var result apiFileJSON
	if err := json.Unmarshal(raw, &result); err != nil || resp.StatusCode != http.StatusOK || result.Description != "A day out.\nWith friends." || result.Visibility != "members" || result.Metadata != "hidden" {
		t.Fatalf("valid combined patch: %s (%v)", raw, err)
	}
	resp, raw = h.apiJSON(http.MethodPatch, path, key, map[string]any{"visibility": "public"})
	if err := json.Unmarshal(raw, &result); err != nil || result.Description == "" {
		t.Fatal("omitted description was cleared")
	}
	other := h.seedUser("bob")
	otherKey := h.seedKey(other.ID, "test", nil)
	if resp, _ := h.apiJSON(http.MethodPatch, path, otherKey, map[string]any{"description": "stolen"}); resp.StatusCode != http.StatusNotFound {
		t.Fatal("another account's API key edited the description")
	}
	resp, raw = h.apiDo(http.MethodPatch, path, key, strings.NewReader("description="), "application/x-www-form-urlencoded")
	if err := json.Unmarshal(raw, &result); err != nil || resp.StatusCode != http.StatusOK || result.Description != "" || result.Visibility != "public" {
		t.Fatalf("form clear: %s (%v)", raw, err)
	}
}
