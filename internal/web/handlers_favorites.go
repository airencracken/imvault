// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"

	"imvault/internal/store"
)

func (s *Server) handleFavorites(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	query := r.URL.Query().Get("q")
	filter := store.FileQuery{FavoritedBy: &user.ID, Search: query}
	if !canModerateContent(user) {
		filter.VisibleTo = &user.ID
	}
	total, err := s.store.CountFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("favorites: count", "error", err)
		s.fail(w, r, http.StatusInternalServerError, "Something went wrong on our side. Please try again.")
		return
	}
	page, pg := pagination(r, total)
	filter.Limit = defaultPageSize
	filter.Offset = (page - 1) * defaultPageSize
	files, err := s.store.ListFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("favorites: list", "error", err)
		s.fail(w, r, http.StatusInternalServerError, "Something went wrong on our side. Please try again.")
		return
	}
	s.renderPage(w, r, http.StatusOK, "favorites", galleryView{
		base:       s.base(r, "Your favorites"),
		Grid:       s.feedGrid(r, files, "No favorites here yet. Open a photo and choose Favorite to save it here."),
		Query:      query,
		Pagination: pg,
	})
}

func (s *Server) handleFavorite(w http.ResponseWriter, r *http.Request) {
	file, ok := s.lookupVisibleFile(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "That form couldn't be read. Please try again.")
		return
	}
	value := r.PostForm.Get("favorite")
	if value != "0" && value != "1" {
		s.fail(w, r, http.StatusBadRequest, "That favorite change isn't valid.")
		return
	}
	favorite := value == "1"
	user := currentUser(r.Context())
	if err := s.store.SetFavorite(r.Context(), user.ID, file.ID, favorite); err != nil {
		s.log.Error("set favorite", "id", file.ID, "error", err)
		s.fail(w, r, http.StatusInternalServerError, "Your favorites couldn't be updated. Please try again.")
		return
	}
	if isHTMX(r) {
		s.renderPartial(w, r, "favorite_button", fileView{
			base: s.base(r, file.OriginalName), File: file, Favorited: favorite,
		})
		return
	}
	http.Redirect(w, r, "/f/"+file.ID, http.StatusSeeOther)
}
