// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"html"
	"strings"
	"testing"
	"time"

	"imvault/internal/metadata"
	"imvault/internal/models"
)

func TestGalleryGroupsDescriptionsAndFilteredNeighbors(t *testing.T) {
	h := newLibraryHarness(t, nil)
	alice, other := h.seedUser("alice"), h.seedUser("other")
	key := h.seedKey(alice.ID, "gallery", nil)
	me := h.sessionFor(t, alice.ID)
	id := h.uploadOne(key, "fixture.png", 12, 13)
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetBlobDetails(t.Context(), file.SHA256, (&metadata.Details{Taken: "2 March 2024 at 12:34"}).Encode()); err != nil {
		t.Fatal(err)
	}
	for index := range defaultPageSize + 2 {
		item := *file
		item.ID = fmt.Sprintf("archive%05d", index)
		item.CreatedAt = time.Unix(int64(index), 0)
		item.Description = "The picnic 100% under the maples"
		item.Visibility = models.VisibilityPublic
		if index == defaultPageSize+1 {
			item.UserID = &other.ID
		}
		if err := h.store.CreateFile(t.Context(), &item); err != nil {
			t.Fatal(err)
		}
	}

	response, page := me.get("/gallery?sort=taken&q=picnic")
	page = html.UnescapeString(page)
	if response.StatusCode != 200 || !strings.Contains(page, "<h2>March 2024</h2>") || strings.Contains(page, "archive00049") || strings.Contains(page, "file-"+id) {
		t.Fatal("description filter or monthly grouping is wrong")
	}
	if !strings.Contains(page, `href="/f/archive00001?from=gallery&q=picnic&sort=taken"`) {
		t.Fatal("gallery file link lost the current filters")
	}
	response, page = me.get("/f/archive00001?from=gallery&q=picnic&sort=taken")
	page = html.UnescapeString(page)
	if response.StatusCode != 200 || !strings.Contains(page, `href="/f/archive00000?from=gallery&page=2&q=picnic&sort=taken">Next photo`) {
		t.Fatal("missing next photo across the page boundary")
	}
	_, page = me.get("/f/archive00000?from=gallery&page=2&q=picnic&sort=taken")
	page = html.UnescapeString(page)
	if !strings.Contains(page, `href="/f/archive00001?from=gallery&q=picnic&sort=taken">Previous photo`) ||
		!strings.Contains(page, `href="/gallery?page=2&q=picnic&sort=taken#file-archive00000"`) ||
		strings.Contains(page, ">Next photo") {
		t.Fatal("wrong previous, next or back link on the last matching photo")
	}
	_, page = h.sessionFor(t, other.ID).get("/f/archive00000?from=gallery&page=2&q=picnic&sort=taken")
	if strings.Contains(page, "Back to gallery") {
		t.Fatal("somebody else's detail page disclosed gallery context")
	}
	_, page = me.get("/gallery?q=picnic+100%25")
	if !strings.Contains(page, "file-archive00001") {
		t.Fatal("literal percent description did not match")
	}
	_, page = me.get("/gallery?q=picnic+100_")
	if strings.Contains(page, "file-archive00001") {
		t.Fatal("underscore was treated as a search wildcard")
	}
}
