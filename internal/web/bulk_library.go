// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"imvault/internal/models"
	"imvault/internal/store"
)

func (s *Server) changeLibrary(r *http.Request, p *params, action string) (int, error) {
	owner, files := currentUser(r.Context()).ID, p.strs("files")
	if action == "visibility" {
		return s.store.SetFilesVisibility(r.Context(), owner, files, models.Visibility(p.str("visibility")))
	}
	if p.str("album") == "new" {
		return s.store.CreateAlbumWithFiles(r.Context(), owner, files, p.str("album_title"))
	}
	album, err := strconv.ParseInt(p.str("album"), 10, 64)
	if err != nil || album <= 0 {
		return 0, fmt.Errorf("%w: choose an album", store.ErrInvalidTags)
	}
	return s.store.AddFilesToAlbum(r.Context(), owner, files, album)
}

func libraryError(err error) (int, string) {
	if errors.Is(err, store.ErrInvalidTags) {
		return 400, err.Error()
	}
	if errors.Is(err, store.ErrNotFound) {
		return 404, "A selected file or album is unavailable. Nothing was changed."
	}
	return 500, "Could not update the selected files. Nothing was changed."
}

func (s *Server) handleBulkLibrary(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not parse the form", 400)
		return
	}
	action := r.PathValue("action")
	if action != "album" && action != "visibility" {
		http.NotFound(w, r)
		return
	}
	count, err := s.changeLibrary(r, &params{form: r.PostForm}, action)
	kind, message := flashNotice, fmt.Sprintf("Updated %d selected files.", count)
	if count == 1 {
		message = "Updated 1 selected file."
	}
	if err != nil {
		_, message = libraryError(err)
		kind = flashError
		s.log.Warn("bulk library", "error", err)
	}
	q := url.Values{}
	s.flashQuery(q, kind, message)
	for _, field := range []string{"q", "page", "sort", "year", "month"} {
		if value := r.PostForm.Get(field); value != "" {
			q.Set(field, value)
		}
	}
	http.Redirect(w, r, "/gallery?"+q.Encode(), http.StatusSeeOther)
}

func (s *Server) apiBulkLibrary(w http.ResponseWriter, r *http.Request) {
	p, err := newParams(r)
	if err != nil {
		writeAPIError(w, 400, err.Error())
		return
	}
	action := r.PathValue("action")
	if action != "album" && action != "visibility" {
		writeAPIError(w, 404, "no such action")
		return
	}
	count, err := s.changeLibrary(r, p, action)
	if err != nil {
		code, message := libraryError(err)
		writeAPIError(w, code, message)
		return
	}
	noStore(w)
	writeJSON(w, 200, map[string]int{"updated": count})
}
