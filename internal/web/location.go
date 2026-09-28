// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/http"
	"strings"

	"imvault/internal/models"
	"imvault/internal/store"
)

func locationFor(r *http.Request, owner *models.User) models.MetadataPolicy {
	if owner == nil {
		return models.MetadataInherit
	}
	return models.ParseMetadataPolicy(r.FormValue("location"))
}

func (p *params) locationPolicy() (*models.MetadataPolicy, error) {
	_, inJSON := p.json["location"]
	_, inForm := p.form["location"]
	if !inJSON && !inForm {
		return nil, nil
	}
	policy := models.MetadataPolicy(strings.ToLower(p.str("location")))
	if !policy.Valid() {
		return nil, fmt.Errorf("location must be shown, inherit, or hidden")
	}
	return &policy, nil
}

func (p *params) locationOr(fallback models.MetadataPolicy) (models.MetadataPolicy, error) {
	policy, err := p.locationPolicy()
	if err != nil {
		return fallback, err
	}
	if policy == nil {
		return fallback, nil
	}
	return *policy, nil
}

func validLocationForm(w http.ResponseWriter, r *http.Request) bool {
	if currentUser(r.Context()) == nil {
		return true
	}
	_, err := (&params{form: r.Form}).locationPolicy()
	if err == nil {
		return true
	}
	if isAPIPath(r) {
		writeAPIError(w, http.StatusBadRequest, err.Error())
	} else {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	return false
}

func (s *Server) handleFileLocation(w http.ResponseWriter, r *http.Request) {
	file, ok := s.loadChangeableFile(w, r)
	if !ok {
		return
	}
	policy := models.MetadataPolicy(strings.ToLower(strings.TrimSpace(r.FormValue("location"))))
	if !policy.Valid() {
		http.Error(w, "location must be shown, inherit, or hidden", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateFile(r.Context(), file.ID, store.FileUpdate{Location: &policy}); err != nil {
		s.log.Error("set location policy", "id", file.ID, "error", err)
		http.Error(w, "could not update location sharing", http.StatusInternalServerError)
		return
	}
	// The location panel and map need to reflect the new setting immediately.
	if isHTMX(r) {
		hxRedirect(w, "/f/"+file.ID)
		return
	}
	redirectNotice(w, r, "/f/"+file.ID, "notice", "Location sharing updated.")
}
