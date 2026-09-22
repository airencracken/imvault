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
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	page, pg := pagination(r, total)
	filter.Limit = defaultPageSize
	filter.Offset = (page - 1) * defaultPageSize
	files, err := s.store.ListFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("favorites: list", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	s.renderPage(w, http.StatusOK, "favorites", galleryView{
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
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}
	value := r.PostForm.Get("favorite")
	if value != "0" && value != "1" {
		http.Error(w, "favorite must be 0 or 1", http.StatusBadRequest)
		return
	}
	favorite := value == "1"
	user := currentUser(r.Context())
	if err := s.store.SetFavorite(r.Context(), user.ID, file.ID, favorite); err != nil {
		s.log.Error("set favorite", "id", file.ID, "error", err)
		http.Error(w, "could not update favorite", http.StatusInternalServerError)
		return
	}
	if isHTMX(r) {
		s.renderPartial(w, "favorite_button", fileView{
			base: s.base(r, file.OriginalName), File: file, Favorited: favorite,
		})
		return
	}
	http.Redirect(w, r, "/f/"+file.ID, http.StatusSeeOther)
}
