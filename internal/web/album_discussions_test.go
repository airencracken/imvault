// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"github.com/airencracken/comfylib/reference"
	"imvault/internal/models"
	"imvault/internal/store"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAlbumPreviewAuthenticationOwnershipAndPrivacy(t *testing.T) {
	h := newHarnessTransport(t, nil, mailOff, false)
	owner := h.seedUser("owner")
	other := h.seedUser("other")
	key := h.seedKey(owner.ID, "key", nil)
	otherKey := h.seedKey(other.ID, "key", nil)
	album, err := h.store.CreateAlbum(t.Context(), owner.ID, store.AlbumInput{Title: "PRIVATE-ALBUM", Visibility: models.VisibilityPrivate})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/albums/" + album.Slug + "/preview"
	request := func(key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		h.srv.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request(""); w.Code != 401 || strings.Contains(w.Body.String(), "PRIVATE-ALBUM") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, k := range []string{key, otherKey} {
		w := request(k)
		if w.Code != 404 || strings.Contains(w.Body.String(), "PRIVATE-ALBUM") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if err = h.store.UpdateAlbum(t.Context(), album.ID, store.AlbumInput{Title: "Public album", Visibility: models.VisibilityPublic}); err != nil {
		t.Fatal(err)
	}
	for _, visibility := range []models.Visibility{models.VisibilityPublic, models.VisibilityPrivate} {
		id := "public-image"
		if visibility == models.VisibilityPrivate {
			id = "private-image"
		}
		file := &models.File{ID: id, UserID: &owner.ID, OriginalName: id + ".jpg", Ext: "jpg", Mime: "image/jpeg", Kind: models.KindImage, Size: 100, Width: 1, Height: 1, SHA256: id, ObjectKey: id, ThumbKey: id, PreviewKey: id, Visibility: visibility, CreatedAt: time.Now()}
		if err := h.store.CreateFile(t.Context(), file); err != nil {
			t.Fatal(err)
		}
		if err := h.store.AddFileToAlbum(t.Context(), album.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	w := request(key)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	var got struct {
		Title      string
		ImageCount int `json:"image_count"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Title != "Public album" || got.ImageCount != 1 {
		t.Fatal(got, err)
	}
	if w = request(otherKey); w.Code != 404 {
		t.Fatal("nonowner metadata", w.Code)
	}
	if err = h.store.UpdateAlbum(t.Context(), album.ID, store.AlbumInput{Title: "PRIVATE-AGAIN", Visibility: models.VisibilityPrivate}); err != nil {
		t.Fatal(err)
	}
	w = request(key)
	if w.Code != 404 || strings.Contains(w.Body.String(), "PRIVATE-AGAIN") {
		t.Fatal("revoked preview leaked", w.Code, w.Body.String())
	}
}
func TestAlbumDraftNeverCopiesPrivateMetadata(t *testing.T) {
	album := &models.Album{Title: "SECRET-TITLE", Description: "SECRET-NOTE", Visibility: models.VisibilityPrivate, FileCount: 999}
	d := albumDraft("https://vault.example/a/secret", album)
	raw, err := reference.Handoff("https://board.example", d)
	if err != nil || strings.Contains(raw, "SECRET-") || strings.Contains(raw, "999") {
		t.Fatal(raw, err)
	}
	u, _ := url.Parse(raw)
	got, err := reference.Read(u.Query())
	if err != nil || got.Source != d.Source {
		t.Fatal(got, err)
	}
}
func TestAlbumDiscussionRoutesRequireSessionAndCSRF(t *testing.T) {
	h := newHarnessTransport(t, nil, mailOff, false)
	for _, suffix := range []string{"discussion", "share"} {
		r := httptest.NewRequest("POST", "/a/secret/"+suffix, strings.NewReader("url=https%3A%2F%2Fboard.example%2Ftopics%2F1"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.srv.Handler().ServeHTTP(w, r)
		if w.Code != 403 && w.Code != 303 {
			t.Fatal(suffix, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "SECRET") {
			t.Fatal("private metadata")
		}
	}
}
