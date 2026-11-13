// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGalleryPutsContentBeforeForms(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	owner := h.sessionFor(t, alice.ID)
	h.uploadOne(h.seedKey(alice.ID, "test", nil), "photo.png", 20, 20)

	resp, page := owner.get("/gallery")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gallery = %d", resp.StatusCode)
	}
	panel := strings.Index(page, `<details class="panel stack bulk-panel" id="bulk-panel">`)
	if panel < 0 {
		t.Fatal("the organise panel is not a collapsed disclosure")
	}
	form := strings.Index(page, `<form id="bulk-tags"`)
	if form < panel {
		t.Fatal("the organise form is not inside its disclosure")
	}
	// The search row stays visible; sorting and dates wait behind a disclosure.
	search := strings.Index(page, `id="gallery-search"`)
	more := strings.Index(page, `<details class="library-more">`)
	if search < 0 || more < 0 || search > more || strings.Index(page, `name="year"`) < more {
		t.Fatal("filters are not collapsed behind one search row")
	}

	_, filtered := owner.get("/gallery?sort=taken")
	if !strings.Contains(filtered, `<details class="library-more" open>`) {
		t.Error("active date filters are hidden behind a closed disclosure")
	}
	_, hostile := owner.get("/gallery?q=%22%3E%3Cscript%3Ealert(1)%3C/script%3E&year=%3Cb%3E")
	if strings.Contains(hostile, "<script>alert(1)</script>") {
		t.Error("the search query was rendered unescaped")
	}
}

func TestAlbumsListComesBeforeTheNewAlbumForm(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	owner := h.sessionFor(t, alice.ID)

	_, empty := owner.get("/albums")
	if !strings.Contains(empty, `<details class="panel stack new-album" open>`) {
		t.Error("with no albums the new album form should start open")
	}
	if resp, _ := owner.post("/albums", url.Values{"title": {"Trip"}, "visibility": {"members"}, "access": {"owner"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create album = %d", resp.StatusCode)
	}
	_, page := owner.get("/albums")
	list := strings.Index(page, "<h2>Your albums</h2>")
	form := strings.Index(page, `<details class="panel stack new-album">`)
	if list < 0 || form < 0 || list > form {
		t.Fatalf("Your albums (%d) does not come before a closed New album disclosure (%d)", list, form)
	}
}
