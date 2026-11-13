// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (s *Server) handleFileRename(w http.ResponseWriter, r *http.Request) {
	file, ok := s.loadChangeableFile(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if !validFileName(name) {
		s.fail(w, r, http.StatusBadRequest, "Use a filename of 1 to 200 characters, without slashes or control characters.")
		return
	}
	if err := s.store.RenameFile(r.Context(), file.ID, name); err != nil {
		s.log.Error("rename file", "id", file.ID, "error", err)
		s.fail(w, r, http.StatusInternalServerError, "The file couldn't be renamed. Please try again.")
		return
	}
	s.redirectFlash(w, r, "/f/"+file.ID, flashNotice, "File renamed.")
}

func validFileName(name string) bool {
	return name != "" && name != "." && name != ".." && utf8.ValidString(name) &&
		utf8.RuneCountInString(name) <= 200 && !strings.ContainsAny(name, "/\\") &&
		strings.IndexFunc(name, unicode.IsControl) == -1
}
