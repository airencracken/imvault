// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/config"
	"imvault/internal/models"
)

func TestUploadCopyLinksForMotion(t *testing.T) {
	h := newLibraryHarness(t, nil)
	for _, tc := range []struct {
		kind  models.Kind
		label string
	}{
		{models.KindVideo, "Poster link"},
		{models.KindAnimated, "Thumbnail link"},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			file := &models.File{ID: "fixture", Kind: tc.kind, OriginalName: "Upload", ThumbKey: "thumbnail.jpg"}
			card := fileCard{File: file, URL: "/f/" + file.ID, UploadOrigin: "https://vault.example"}
			var rendered bytes.Buffer
			if err := h.srv.render.partial(&rendered, "upload-share-links", card); err != nil {
				t.Fatal(err)
			}
			body := rendered.String()
			if !strings.Contains(body, "<summary>"+tc.label+"</summary>") ||
				!strings.Contains(body, `value="`+card.UploadOrigin+file.ThumbURL()+`" data-comfy-copy`) ||
				strings.Contains(body, "/preview?") {
				t.Fatalf("incorrect rendition control: %s", body)
			}
		})
	}
}

func TestUploadCopyLinksRespectVisibility(t *testing.T) {
	const origin = "https://share.example"
	h := newLibraryHarness(t, func(cfg *config.Config) { cfg.BaseURL = origin })
	owner, reader := h.seedUser("alice"), h.seedUser("reader")
	me, other := h.sessionFor(t, owner.ID), h.sessionFor(t, reader.ID)
	data := pngFixture(t, 21, 17)
	for _, tc := range []struct {
		visibility models.Visibility
		guest      int
		member     int
	}{
		{models.VisibilityPrivate, http.StatusNotFound, http.StatusNotFound},
		{models.VisibilityMembers, http.StatusNotFound, http.StatusOK},
		{models.VisibilityPublic, http.StatusOK, http.StatusOK},
	} {
		t.Run(string(tc.visibility), func(t *testing.T) {
			fields := url.Values{"visibility": {string(tc.visibility)}}
			resp, body := postNoJS(t, me, fields, `test"><script>.png`, data)
			start := strings.Index(body, `id="results"`)
			if resp.StatusCode != http.StatusOK || start < 0 {
				t.Fatalf("upload = %d: %s", resp.StatusCode, truncate(body))
			}
			id := firstFileID(t, body[start:])
			file, err := h.store.FileByID(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			page := "/f/" + id
			preview := file.PreviewURL()
			for _, expected := range []string{
				`>Page link</label>`, `>Preview link</summary>`, tc.visibility.Explain(),
				`readonly value="` + origin + page + `" data-comfy-copy`,
				`readonly value="` + origin + preview + `" data-comfy-copy`,
			} {
				if !strings.Contains(body, expected) {
					t.Errorf("upload results are missing %q", expected)
				}
			}
			if strings.Count(body, " data-comfy-copy ") != 2 || strings.Contains(body, `test"><script>`) {
				t.Fatal("unexpected copy controls or unescaped upload name")
			}
			for _, path := range []string{page, preview} {
				if resp, _ := me.get(path); resp.StatusCode != http.StatusOK {
					t.Errorf("owner GET %s = %d", path, resp.StatusCode)
				}
				if resp, _ := other.get(path); resp.StatusCode != tc.member {
					t.Errorf("member GET %s = %d, want %d", path, resp.StatusCode, tc.member)
				}
				if resp, _ := h.apiDo(http.MethodGet, path, "", nil, ""); resp.StatusCode != tc.guest {
					t.Errorf("guest GET %s = %d, want %d", path, resp.StatusCode, tc.guest)
				}
			}
		})
	}
	if resp, body := me.get("/gallery"); resp.StatusCode != http.StatusOK || strings.Contains(body, "upload-share-links") {
		t.Fatalf("upload controls leaked into the gallery: %d", resp.StatusCode)
	}
	if resp, body := me.upload(map[string]string{}, []uploadFile{{name: "invalid.png", data: []byte("invalid")}}); resp.StatusCode != http.StatusOK ||
		!strings.Contains(body, `data-failed="1"`) || strings.Contains(body, "upload-share-links") {
		t.Fatalf("failed upload offered share links: %d", resp.StatusCode)
	}
}
