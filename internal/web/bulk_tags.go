// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"imvault/internal/store"
)

func (s *Server) handleBulkTags(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not parse the form", http.StatusBadRequest)
		return
	}
	count, err := s.store.AddTagsToFiles(r.Context(), currentUser(r.Context()).ID, r.PostForm["files"], strings.Split(r.PostForm.Get("tags"), ","))
	kind, message := flashNotice, fmt.Sprintf("Tags added to %d files.", count)
	if count == 1 {
		message = "Tags added to 1 file."
	}
	if err != nil {
		_, message = bulkTagsError(err)
		kind = flashError
		s.log.Warn("bulk tags", "error", err)
	}
	q := url.Values{}
	s.flashQuery(q, kind, message)
	for _, field := range []string{"q", "page"} {
		if value := r.PostForm.Get(field); value != "" {
			q.Set(field, value)
		}
	}
	http.Redirect(w, r, "/gallery?"+q.Encode(), http.StatusSeeOther)
}

func (s *Server) apiBulkTags(w http.ResponseWriter, r *http.Request) {
	params, err := newParams(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	count, err := s.store.AddTagsToFiles(r.Context(), currentUser(r.Context()).ID, params.strs("files"), params.strs("tags"))
	if err != nil {
		status, message := bulkTagsError(err)
		writeAPIError(w, status, message)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{"tagged": count})
}

func bulkTagsError(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrInvalidTags):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "One or more selected files are unavailable. No tags were added."
	default:
		return http.StatusInternalServerError, "Could not add tags. No tags were added."
	}
}
