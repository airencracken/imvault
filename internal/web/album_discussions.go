// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"github.com/airencracken/comfylib/reference"
	"imvault/internal/models"
	"net/http"
)

func (s *Server) handleAlbumDiscussion(w http.ResponseWriter, r *http.Request) {
	album, ok := s.loadAlbum(w, r)
	if !ok {
		return
	}
	user := currentUser(r.Context())
	if !ownsAlbum(user, album) {
		s.notFound(w, r, "No album by that name.")
		return
	}
	raw := r.PostForm.Get("url")
	if _, err := reference.URL(raw); err != nil {
		http.Error(w, "Use an absolute HTTP(S) discussion link without credentials.", 400)
		return
	}
	if err := s.store.LinkAlbumDiscussion(r.Context(), user.ID, album.ID, raw, r.PostForm.Get("remove") == "1"); err != nil {
		http.Error(w, "Could not save discussion link.", 500)
		return
	}
	http.Redirect(w, r, "/a/"+album.Slug, 303)
}

// Private albums carry only a source link, even when the owner starts a draft.
func albumDraft(source string, album *models.Album) reference.Draft {
	return reference.Draft{Source: source, Title: "An imvault album", Body: "An album in imvault. Its access rules still apply."}
}
func (s *Server) handleAlbumShare(w http.ResponseWriter, r *http.Request) {
	album, ok := s.loadAlbum(w, r)
	if !ok {
		return
	}
	if !canViewAlbum(currentUser(r.Context()), album) {
		s.notFound(w, r, "No album by that name.")
		return
	}
	if s.cfg.WitmootURL == "" || s.cfg.BaseURL == "" {
		http.Error(w, "Configure IMVAULT_BASE_URL and IMVAULT_WITMOOT_URL to prepare a discussion.", 422)
		return
	}
	draft := albumDraft(s.absoluteURL(r, "/a/"+album.Slug), album)
	// Titles are fetched on demand in Witmoot, so later privacy changes apply.
	link, err := reference.Handoff(s.cfg.WitmootURL, draft)
	if err != nil {
		http.Error(w, "Invalid discussion server configuration.", 500)
		return
	}
	s.renderPage(w, 200, "album_handoff", struct {
		base
		Handoff string
	}{s.base(r, "An album discussion"), link})
}

// Bearer authentication and ownership are required even for public previews.
// No private title, count, cover, description or member-file detail is returned.
func (s *Server) apiAlbumPreview(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	album, err := s.ownedAlbum(r.Context(), currentUser(r.Context()), r.PathValue("ref"))
	if err != nil {
		s.writeAlbumLookupError(w, err)
		return
	}
	summary, err := s.store.PublicAlbumPreview(r.Context(), currentUser(r.Context()).ID, album.ID, currentUser(r.Context()).IsAdmin())
	if err != nil {
		s.writeAlbumLookupError(w, err)
		return
	}

	writeJSON(w, 200, map[string]any{"title": summary.Title, "slug": summary.Slug, "visibility": "public", "image_count": summary.ImageCount, "page_url": s.absoluteURL(r, "/a/"+summary.Slug)})
}
