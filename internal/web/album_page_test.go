// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"imvault/internal/models"
	"imvault/internal/store"
)

const albumPageBlob = "album-page-test-blob"

func setupAlbumPage(t *testing.T) (*harness, *models.User, *models.Album) {
	t.Helper()
	h := newHarness(t)
	owner := h.seedUser("album-owner")
	album, err := h.store.CreateAlbum(t.Context(), owner.ID, store.AlbumInput{
		Title: "Big Trip", Visibility: models.VisibilityPublic, Access: models.AlbumAccessMembers,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.EnsureBlob(t.Context(), albumPageBlob, 64, "original", "thumb", "preview", ""); err != nil {
		t.Fatal(err)
	}
	return h, owner, album
}

func seedAlbumPageFile(t *testing.T, h *harness, ownerID int64, id string, created time.Time, visibility models.Visibility, expires *time.Time, album *models.Album) {
	t.Helper()
	file := &models.File{
		ID: id, UserID: &ownerID, OriginalName: id + ".png",
		Ext: "png", Mime: "image/png", Kind: models.KindImage, Size: 64,
		SHA256: albumPageBlob, Visibility: visibility, CreatedAt: created, ExpiresAt: expires,
	}
	if err := h.store.CreateFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if album != nil {
		if err := h.store.AddFileToAlbum(t.Context(), album.ID, file.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAlbumPagePaginatesVisibleFiles(t *testing.T) {
	h, owner, album := setupAlbumPage(t)
	ownerSession := h.sessionFor(t, owner.ID)
	member := h.sessionFor(t, h.seedUser("reader").ID)
	now := time.Now().UTC()
	expired := now.Add(-time.Hour)

	// More than both the gallery page size and the old implicit 60-file limit.
	const publicTotal = 61
	for i := 0; i < publicTotal; i++ {
		id := fmt.Sprintf("photo-%02d", i)
		seedAlbumPageFile(t, h, owner.ID, id, now, models.VisibilityPublic, nil, album)
	}
	seedAlbumPageFile(t, h, owner.ID, "member-image", now.Add(time.Second), models.VisibilityMembers, nil, album)
	seedAlbumPageFile(t, h, owner.ID, "private-image", now.Add(time.Second), models.VisibilityPrivate, nil, album)
	seedAlbumPageFile(t, h, owner.ID, "expired-image", now, models.VisibilityPublic, &expired, album)
	seedAlbumPageFile(t, h, owner.ID, "trashed-image", now, models.VisibilityPublic, nil, album)
	if err := h.store.TrashFile(t.Context(), "trashed-image", owner.ID, now); err != nil {
		t.Fatal(err)
	}

	path := "/a/" + album.Slug
	for _, view := range []struct {
		name  string
		get   func(string) (*http.Response, string)
		count int
	}{
		{"guest", h.get, publicTotal},
		{"member", member.get, publicTotal + 1},
		{"owner", ownerSession.get, publicTotal + 2},
	} {
		t.Run(view.name, func(t *testing.T) {
			first, page1 := view.get(path)
			if first.StatusCode != http.StatusOK || strings.Count(page1, `<figure class="card"`) != defaultPageSize {
				t.Fatalf("page 1 status = %d, cards = %d", first.StatusCode, strings.Count(page1, `<figure class="card"`))
			}
			if !strings.Contains(page1, fmt.Sprintf("<li>%d files</li>", view.count)) || !strings.Contains(page1, `href="?page=2"`) {
				t.Errorf("page 1 did not show %d files and a link to page 2", view.count)
			}
			if strings.Contains(page1, "member-image") != (view.name != "guest") ||
				strings.Contains(page1, "private-image") != (view.name == "owner") ||
				strings.Contains(page1, "expired-image") || strings.Contains(page1, "trashed-image") {
				t.Error("page 1 has the wrong visibility or retention scope")
			}

			last, page2 := view.get(path + "?page=2")
			wantLast := view.count - defaultPageSize
			if last.StatusCode != http.StatusOK || strings.Count(page2, `<figure class="card"`) != wantLast || !strings.Contains(page2, `id="file-photo-00"`) {
				t.Errorf("page 2 status = %d, cards = %d, want %d and the oldest file", last.StatusCode, strings.Count(page2, `<figure class="card"`), wantLast)
			}
			if strings.Contains(page1, `id="file-photo-00"`) || strings.Contains(page2, `id="file-photo-60"`) {
				t.Error("the two pages overlap")
			}
			for _, invalid := range []string{"-1", "bad", "99999999"} {
				resp, body := view.get(path + "?page=" + invalid)
				want := "60"
				if invalid == "99999999" {
					want = "00"
				}
				if resp.StatusCode != http.StatusOK || !strings.Contains(body, `id="file-photo-`+want+`"`) {
					t.Errorf("invalid page parameter %q was not clamped", invalid)
				}
			}
		})
	}

	for _, listing := range []struct {
		get   func(string) (*http.Response, string)
		count int
	}{{member.get, publicTotal + 1}, {ownerSession.get, publicTotal + 2}} {
		resp, body := listing.get("/albums")
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, fmt.Sprintf("<span>%d files</span>", listing.count)) {
			t.Errorf("album index status = %d, did not show %d visible files", resp.StatusCode, listing.count)
		}
	}

	remove := path + "/files/photo-00/delete?page=2"
	resp, _ := ownerSession.post(remove, url.Values{})
	to, err := resp.Location()
	if err != nil || resp.StatusCode != http.StatusSeeOther || to.Path != path || to.Query().Get("page") != "2" {
		t.Fatalf("removal from page 2 returned %d, %v, %v", resp.StatusCode, to, err)
	}
	if _, err := h.store.FileByID(t.Context(), "photo-00"); err != nil {
		t.Fatalf("removing the album membership removed the file: %v", err)
	}
	_, after := ownerSession.get(path + "?page=2")
	if strings.Contains(after, path+"/files/photo-00/delete") || !strings.Contains(after, `value="photo-00"`) {
		t.Error("removed file should move from the album grid back into the gallery picker")
	}
}

func TestAlbumPickerExcludesCurrentMembersBeforeLimiting(t *testing.T) {
	h, owner, album := setupAlbumPage(t)
	other := h.seedUser("another-reader")
	now := time.Now().UTC()
	for i := 0; i < albumFileLimit; i++ {
		seedAlbumPageFile(t, h, owner.ID, fmt.Sprintf("included-%03d", i), now, models.VisibilityPublic, nil, album)
	}
	seedAlbumPageFile(t, h, owner.ID, "older-candidate", now.Add(-time.Hour), models.VisibilityPrivate, nil, nil)
	seedAlbumPageFile(t, h, other.ID, "not-mine", now, models.VisibilityPublic, nil, nil)

	resp, body := h.sessionFor(t, owner.ID).get("/a/" + album.Slug)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `value="older-candidate"`) {
		t.Fatalf("picker status = %d, missing an eligible older file", resp.StatusCode)
	}
	if strings.Contains(body, `value="included-`) || strings.Contains(body, `value="not-mine"`) {
		t.Error("picker offered files that cannot be added")
	}
}
