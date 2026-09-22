// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"imvault/internal/models"
	"imvault/internal/store"
)

func (s *Server) handleFileDescription(w http.ResponseWriter, r *http.Request) {
	file, ok := s.loadChangeableFile(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil || !r.PostForm.Has("description") {
		http.Error(w, "provide a description, or an empty value to clear it", http.StatusBadRequest)
		return
	}
	text, err := models.NormalizeFileDescription(r.PostForm.Get("description"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateFile(r.Context(), file.ID, store.FileUpdate{Description: &text}); err != nil {
		s.log.Error("set file description", "id", file.ID, "error", err)
		http.Error(w, "could not save the description", http.StatusInternalServerError)
		return
	}
	redirectNotice(w, r, "/f/"+file.ID, "notice", "Description saved.")
}

func (p *params) description() (*string, error) {
	var value *string
	if p.json != nil {
		raw, present := p.json["description"]
		if !present {
			return nil, nil
		}
		if err := json.Unmarshal(raw, &value); err != nil || value == nil {
			return nil, errors.New("description must be a string; use an empty string to clear it")
		}
	} else {
		if !p.form.Has("description") {
			return nil, nil
		}
		text := p.form.Get("description")
		value = &text
	}
	text, err := models.NormalizeFileDescription(*value)
	return &text, err
}
