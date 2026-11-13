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
		s.fail(w, r, http.StatusBadRequest, "Send a description, or an empty one to remove it.")
		return
	}
	text, err := models.NormalizeFileDescription(r.PostForm.Get("description"))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, sentence(err.Error()))
		return
	}
	if err := s.store.UpdateFile(r.Context(), file.ID, store.FileUpdate{Description: &text}); err != nil {
		s.log.Error("set file description", "id", file.ID, "error", err)
		s.fail(w, r, http.StatusInternalServerError, "The description couldn't be saved. Please try again.")
		return
	}
	s.redirectFlash(w, r, "/f/"+file.ID, flashNotice, "Description saved.")
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
