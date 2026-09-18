// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"imvault/internal/ids"
	"imvault/internal/models"
)

// seedFileWithTag creates a file owned by owner and labels it, without going
// through the upload pipeline. Tags belong to the file's owner, so owner must
// not be nil.
func seedFileWithTag(t *testing.T, h *harness, tag string, owner *int64, public bool) string {
	t.Helper()

	if owner == nil {
		t.Fatal("seedFileWithTag needs an owner: anonymous uploads have no tag namespace")
	}
	id := ids.New(12)
	file := &models.File{
		ID:           id,
		UserID:       owner,
		OriginalName: id + ".png",
		Ext:          "png",
		Mime:         "image/png",
		Size:         128,
		Width:        16,
		Height:       16,
		SHA256:       id,
		ObjectKey:    "orig/" + id + ".png",
		ThumbKey:     "thumb/" + id + ".png",
		Kind:         models.KindImage,
		IsPublic:     public,
		CreatedAt:    time.Now().UTC(),
	}
	if err := h.store.CreateFile(t.Context(), file); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := h.store.AddTag(t.Context(), id, *owner, tag); err != nil {
		t.Fatalf("seed tag: %v", err)
	}
	return id
}

// getAnonymous fetches a path with no cookies, as a logged-out visitor would.
func getAnonymous(t *testing.T, url string) (int, string) {
	t.Helper()

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("anonymous GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, string(body)
}

func TestTagIndexRespectsFileVisibility(t *testing.T) {
	h := newHarness(t)

	marcus := h.registerForm("marcus")
	other := h.seedUser("other")

	seedFileWithTag(t, h, "mytag", &marcus.ID, false)
	seedFileWithTag(t, h, "other-secret", &other.ID, false)
	seedFileWithTag(t, h, "public-tag", &other.ID, true)

	// Signed in: Marcus sees his own private tag and the public one, but not
	// the other account's private tag.
	resp, page := h.get("/tags")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tags page = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(page, "mytag") {
		t.Error("the owner cannot see their own private tag")
	}
	if !strings.Contains(page, "public-tag") {
		t.Error("a public file's tag is missing")
	}
	if strings.Contains(page, "other-secret") {
		t.Error("the tag index leaks another account's private tag")
	}

	// Anonymous: only the public file's tag.
	status, body := getAnonymous(t, h.server.URL+"/tags")
	if status != http.StatusOK {
		t.Fatalf("anonymous tags page = %d, want 200", status)
	}
	if strings.Contains(body, "mytag") || strings.Contains(body, "other-secret") {
		t.Error("the anonymous tag index leaks private tags")
	}
	if !strings.Contains(body, "public-tag") {
		t.Error("the anonymous tag index is missing a public tag")
	}
}

func TestTagPageHidesInvisibleTags(t *testing.T) {
	h := newHarness(t)

	marcus := h.registerForm("marcus")
	other := h.seedUser("other")

	seedFileWithTag(t, h, "mytag", &marcus.ID, false)
	seedFileWithTag(t, h, "other-secret", &other.ID, false)

	// Tags are addressed by owner, so another account's tag is simply not
	// reachable, exactly like a tag that does not exist.
	if resp, _ := h.get("/tags/other/other-secret"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("invisible tag page = %d, want 404", resp.StatusCode)
	}
	if resp, _ := h.get("/tags/marcus/mytag"); resp.StatusCode != http.StatusOK {
		t.Errorf("owner's tag page = %d, want 200", resp.StatusCode)
	}
	if status, _ := getAnonymous(t, h.server.URL+"/tags/marcus/mytag"); status != http.StatusNotFound {
		t.Errorf("anonymous tag page = %d, want 404", status)
	}
}

func TestAPITagIndexRespectsVisibility(t *testing.T) {
	h := newHarness(t)

	alice := h.seedUser("alice")
	bob := h.seedUser("bob")
	aliceKey := h.seedKey(alice.ID, "alice", nil)
	bobKey := h.seedKey(bob.ID, "bob", nil)

	seedFileWithTag(t, h, "alice-secret", &alice.ID, false)
	seedFileWithTag(t, h, "shared-tag", &alice.ID, true)

	// Bob's index contains the public tag but not Alice's private one.
	resp, raw := h.apiJSON(http.MethodGet, "/api/v1/tags", bobKey, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tag index = %d, want 200", resp.StatusCode)
	}
	tags := decodeTags(t, raw)
	if !hasTagNamed(tags, "shared-tag") {
		t.Error("the tag on a public file is missing from the index")
	}
	if hasTagNamed(tags, "alice-secret") {
		t.Error("the tag index leaks a tag from another account's private file")
	}

	// Alice sees her own.
	_, raw = h.apiJSON(http.MethodGet, "/api/v1/tags", aliceKey, nil)
	if !hasTagNamed(decodeTags(t, raw), "alice-secret") {
		t.Error("the owner cannot see their own private tag")
	}

	// Filtering is scoped to the caller's own namespace: this endpoint lists
	// their uploads, so another account's tag can never select anything.
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/files?tag=alice-secret", bobKey, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("bob filtering by alice's tag = %d, want 404", resp.StatusCode)
	}
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/files?tag=shared-tag", bobKey, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("bob filtering by alice's public tag = %d, want 404", resp.StatusCode)
	}
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/files?tag=alice-secret", aliceKey, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("alice filtering by her own tag = %d, want 200", resp.StatusCode)
	}
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/files?tag=shared-tag", aliceKey, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("alice filtering by her public tag = %d, want 200", resp.StatusCode)
	}
}

func TestAPIRemovingATagCannotProbeRemoteTagNames(t *testing.T) {
	h := newHarness(t)

	alice := h.seedUser("alice")
	bob := h.seedUser("bob")
	aliceKey := h.seedKey(alice.ID, "alice", nil)
	bobKey := h.seedKey(bob.ID, "bob", nil)

	seedFileWithTag(t, h, "alice-secret", &alice.ID, false)
	bobFile := seedFileWithTag(t, h, "bob-tag", &bob.ID, false)

	// "alice-secret" exists elsewhere, but not on Bob's file, so the response
	// must not confirm that it exists at all.
	resp, raw := h.apiJSON(http.MethodDelete, "/api/v1/files/"+bobFile+"/tags/alice-secret", bobKey, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("probing a remote tag name = %d, want 404 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}

	// Bob can still remove his own tag, by name and by slug.
	if resp, _ := h.apiJSON(http.MethodDelete, "/api/v1/files/"+bobFile+"/tags/bob-tag", bobKey, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("removing an own tag by slug = %d, want 200", resp.StatusCode)
	}

	// Alice's tag is untouched by any of this.
	_, raw = h.apiJSON(http.MethodGet, "/api/v1/tags", aliceKey, nil)
	if !hasTagNamed(decodeTags(t, raw), "alice-secret") {
		t.Error("alice's tag was affected by bob's request")
	}
}
