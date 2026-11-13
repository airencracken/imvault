// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"github.com/airencracken/comfylib/reference"
	"github.com/airencracken/comfylib/suite"
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
	if token := r.PostForm.Get("discussion_state"); token != "" && !suite.CheckDiscussion(token, "/a/"+album.Slug, s.discussionSession(r), raw, s.cfg.WitmootURL) {
		s.fail(w, r, 400, "This link has expired. Start a new discussion draft from the album.")
		return
	}
	if _, err := reference.URL(raw); err != nil {
		s.fail(w, r, 400, "Use a full https:// or http:// discussion link without a username or password.")
		return
	}
	if err := s.store.LinkAlbumDiscussion(r.Context(), user.ID, album.ID, raw, r.PostForm.Get("remove") == "1"); err != nil {
		s.log.Error("link album discussion", "album", album.ID, "error", err)
		s.fail(w, r, 500, "The discussion link couldn't be saved. Please try again.")
		return
	}
	suite.FinishDiscussion(r.PostForm.Get("discussion_state"))
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
		s.log.Warn("album discussions need IMVAULT_BASE_URL and IMVAULT_WITMOOT_URL", "album", album.ID)
		s.fail(w, r, 422, "Discussions aren't set up on this site yet.")
		return
	}
	draft := albumDraft(s.absoluteURL(r, "/a/"+album.Slug), album)
	// Titles are fetched on demand in Witmoot, so later privacy changes apply.
	link, err := reference.Handoff(s.cfg.WitmootURL, draft)
	if err != nil {
		s.log.Error("prepare discussion handoff", "album", album.ID, "error", err)
		s.fail(w, r, 500, "Discussions aren't set up on this site yet.")
		return
	}
	if ownsAlbum(currentUser(r.Context()), album) {
		link, err = suite.PrepareDiscussion(link, draft.Source, s.discussionSession(r))
		if err != nil {
			s.fail(w, r, 429, "Too many discussions are being prepared. Please wait a moment and try again.")
			return
		}
	}
	s.renderPage(w, r, 200, "album_handoff", struct {
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

func (s *Server) discussionSession(r *http.Request) string {
	c, err := r.Cookie(s.cookieName(r, sessionCookie))
	if err != nil {
		return ""
	}
	return c.Value
}
func (s *Server) handleAlbumDiscussionReturn(w http.ResponseWriter, r *http.Request) {
	album, ok := s.loadAlbum(w, r)
	if !ok {
		return
	}
	if !ownsAlbum(currentUser(r.Context()), album) {
		s.notFound(w, r, "No album by that name.")
		return
	}
	path := "/a/" + album.Slug
	q := r.URL.Query()
	if len(q["state"]) != 1 || len(q["thread"]) != 1 || !suite.CheckDiscussion(q.Get("state"), path, s.discussionSession(r), q.Get("thread"), s.cfg.WitmootURL) {
		s.fail(w, r, 400, "This link has expired. Start a new discussion draft from the album.")
		return
	}
	suite.RenderDiscussion(w, path+"/discussion", path, q.Get("thread"), q.Get("state"), "csrf_token", csrfToken(r.Context()), "/static/comfyware/components.css")
}
