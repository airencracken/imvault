// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/store"
)

// anonymousUpload posts a file with no session at all, the way a visitor does.
func anonymousUpload(t *testing.T, h *harness, fields map[string]string, files ...uploadFile) (*session, string) {
	t.Helper()

	anon := h.newSession(t)
	resp, body := anon.upload(fields, files)
	if resp.StatusCode != 200 {
		t.Fatalf("anonymous upload = %d, want 200", resp.StatusCode)
	}
	return anon, body
}

func TestAnonymousUploadCanCarryTagsAtUploadTime(t *testing.T) {
	h := newHarness(t)

	_, body := anonymousUpload(t, h,
		map[string]string{"public": "1", "tags": "holiday, beach"},
		uploadFile{name: "sea.png", data: pngFixture(t, 40, 40)},
	)
	id := firstFileID(t, body)

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if file.UserID != nil {
		t.Fatal("the upload was attributed to somebody")
	}

	tags := file.Tags
	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2: %+v", len(tags), tags)
	}

	// Both live in the shared namespace, which has no owning account.
	for _, tag := range tags {
		if !tag.Anonymous() {
			t.Errorf("tag %q was given an owner", tag.Name)
		}
	}

	names := []string{tags[0].Name, tags[1].Name}
	for _, want := range []string{"holiday", "beach"} {
		var found bool
		for _, name := range names {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("tag %q is missing from %v", want, names)
		}
	}

	// And they are addressable at the reserved owner segment.
	page, rendered := h.get("/tags/" + "~" + "/holiday")
	if page.StatusCode != 200 {
		t.Fatalf("the anonymous tag page = %d, want 200", page.StatusCode)
	}
	if !strings.Contains(rendered, "file-"+id) {
		t.Errorf("the anonymous tag page does not list the upload: %s", truncate(rendered))
	}
	if !strings.Contains(rendered, "anonymous") {
		t.Error("the page does not say the tag belongs to the anonymous namespace")
	}
}

func TestAnonymousTagsAppearInTheIndex(t *testing.T) {
	h := newHarness(t)

	_, body := anonymousUpload(t, h,
		map[string]string{"public": "1", "tags": "seaside"},
		uploadFile{name: "sea.png", data: pngFixture(t, 40, 40)},
	)
	_ = body

	// Anonymous uploads are public, so their tags are visible to anyone.
	anon := h.newSession(t)
	resp, page := anon.get("/tags")
	if resp.StatusCode != 200 {
		t.Fatalf("tags page = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(page, "seaside") {
		t.Errorf("the index does not show the anonymous tag: %s", truncate(page))
	}
	if !strings.Contains(page, "~%2Fseaside") && !strings.Contains(page, "~/seaside") {
		t.Errorf("the index does not link to the anonymous namespace: %s", truncate(page))
	}

	// A signed-in account sees it too, because the file is public.
	h.registerForm("boss")
	_, page = h.get("/tags")
	if !strings.Contains(page, "seaside") {
		t.Error("a signed-in viewer cannot see a public anonymous tag")
	}
}

func TestAnonymousNamespacesAreSeparateFromAccounts(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	// The account tags its own upload "shared-name".
	resp, body := h.uploadFiles(map[string]string{"public": "1", "tags": "shared-name"},
		[]uploadFile{{name: "mine.png", data: pngFixture(t, 40, 40)}})
	if resp.StatusCode != 200 {
		t.Fatalf("upload = %d", resp.StatusCode)
	}
	ownedID := firstFileID(t, body)

	// An anonymous upload uses the same word.
	_, body = anonymousUpload(t, h,
		map[string]string{"public": "1", "tags": "shared-name"},
		uploadFile{name: "theirs.png", data: pngFixture(t, 48, 48)},
	)
	anonID := firstFileID(t, body)

	owned, err := h.store.FileByID(t.Context(), ownedID)
	if err != nil {
		t.Fatal(err)
	}
	anonFile, err := h.store.FileByID(t.Context(), anonID)
	if err != nil {
		t.Fatal(err)
	}

	if len(owned.Tags) != 1 || len(anonFile.Tags) != 1 {
		t.Fatalf("expected one tag each, got %+v and %+v", owned.Tags, anonFile.Tags)
	}

	// Same name, two namespaces, two rows: neither can see or delete the other.
	if owned.Tags[0].ID == anonFile.Tags[0].ID {
		t.Fatal("the two namespaces shared a tag row")
	}
	if owned.Tags[0].Anonymous() {
		t.Error("the account's tag landed in the anonymous namespace")
	}
	if !anonFile.Tags[0].Anonymous() {
		t.Error("the anonymous tag was given an owner")
	}
}

func TestAnonymousUploadCannotBeTaggedAfterwards(t *testing.T) {
	h := newHarness(t)

	anon, body := anonymousUpload(t, h,
		map[string]string{"public": "1"},
		uploadFile{name: "sea.png", data: pngFixture(t, 40, 40)},
	)
	id := firstFileID(t, body)

	// The visitor who uploaded it has no account, so there is no route that
	// would let them come back and tag it.
	resp, _ := anon.post("/f/"+id+"/tags", url.Values{"name": {"later"}})
	if resp.StatusCode != 303 {
		t.Fatalf("tagging after the fact = %d, want a redirect to the login page", resp.StatusCode)
	}

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Tags) != 0 {
		t.Errorf("a tag was added after the fact: %+v", file.Tags)
	}
}

func TestAdministratorCanTagAnAnonymousUploadAfterwards(t *testing.T) {
	h := newHarness(t)

	_, body := anonymousUpload(t, h,
		map[string]string{"public": "1", "tags": "arrived"},
		uploadFile{name: "sea.png", data: pngFixture(t, 40, 40)},
	)
	id := firstFileID(t, body)

	// The administrator signs in and opens the file.
	h.registerForm("boss")

	resp, page := h.get("/f/" + id)
	if resp.StatusCode != 200 {
		t.Fatalf("file page = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(page, "add a tag") {
		t.Fatalf("an administrator is not offered the tag form: %s", truncate(page))
	}

	resp, chips := h.postHTMX("/f/"+id+"/tags", url.Values{
		"csrf_token": {h.csrf()},
		"name":       {"reviewed"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("admin tag = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(chips, "reviewed") {
		t.Errorf("the tag was not added: %s", truncate(chips))
	}

	// It joined the shared namespace, not the administrator's own.
	tags, err := h.store.TagsForFile(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}

	var added bool
	for _, tag := range tags {
		if tag.Name == "reviewed" {
			added = true
			if !tag.Anonymous() {
				t.Error("the administrator's tag was filed under their own account")
			}
		}
	}
	if !added {
		t.Fatalf("the tag is missing: %+v", tags)
	}

	// And the administrator still has no tag of their own by that name.
	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.TagByRefInNamespace(t.Context(), &boss.ID, "reviewed"); !errors.Is(err, store.ErrNotFound) {
		t.Error("the tag was filed in the administrator's namespace")
	}
}

func TestAnonymousTagsArePrunedWithTheUpload(t *testing.T) {
	h := newHarness(t)

	_, body := anonymousUpload(t, h,
		map[string]string{"public": "1", "tags": "fleeting"},
		uploadFile{name: "sea.png", data: pngFixture(t, 40, 40)},
	)
	id := firstFileID(t, body)

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing else carries it, so removing it leaves the tag with no files.
	if err := h.store.RemoveTag(t.Context(), id, file.Tags[0].ID); err != nil {
		t.Fatal(err)
	}

	if _, err := h.store.TagByRefInNamespace(t.Context(), nil, "fleeting"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an orphaned anonymous tag survived: %v", err)
	}
}

func TestAPICanTagAtUploadTime(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	anon, body := anonymousUpload(t, h,
		map[string]string{"public": "1", "tags": "one, two"},
		uploadFile{name: "sea.png", data: pngFixture(t, 40, 40)},
	)
	_ = anon
	id := firstFileID(t, body)

	tags, err := h.store.TagsForFile(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2", len(tags))
	}
}
