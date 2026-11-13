// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"imvault/internal/models"
	"imvault/internal/store"
)

// The two sharing choices a file has. The API and the forms call them by the
// names they have always had.
const (
	cameraField   = "metadata"
	locationField = "location"
)

// shown reads one sharing choice. Absent is nil. A form may send the field
// twice, a hidden "hidden" followed by a checked "shown", so that unchecking a
// box still says something; any "shown" among the values wins.
func (p *params) shown(key string) (*bool, error) {
	var raws []string
	if p.json != nil {
		raw, ok := p.json[key]
		if !ok {
			return nil, nil
		}
		var b *bool
		if err := json.Unmarshal(raw, &b); err == nil && b != nil {
			return b, nil
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("%s must be shown or hidden", key)
		}
		raws = []string{s}
	} else {
		values, ok := p.form[key]
		if !ok {
			return nil, nil
		}
		raws = values
	}
	if len(raws) == 0 {
		return nil, fmt.Errorf("%s must be shown or hidden", key)
	}
	result := false
	for _, raw := range raws {
		value, ok := models.ParseShown(raw)
		if !ok {
			return nil, fmt.Errorf("%s must be shown or hidden", key)
		}
		result = result || value
	}
	return &result, nil
}

// sharingChoices reads both choices, either of which may be absent.
func (p *params) sharingChoices() (camera, location *bool, err error) {
	camera, err = p.shown(cameraField)
	if err != nil {
		return nil, nil, err
	}
	location, err = p.shown(locationField)
	if err != nil {
		return nil, nil, err
	}
	return camera, location, nil
}

// validSharingForm refuses an upload whose sharing choices cannot be read,
// before any bytes are stored. Anonymous uploads ignore both fields.
func (s *Server) validSharingForm(w http.ResponseWriter, r *http.Request) bool {
	if currentUser(r.Context()) == nil {
		return true
	}
	if _, _, err := (&params{form: r.Form}).sharingChoices(); err != nil {
		if isAPIPath(r) {
			writeAPIError(w, http.StatusBadRequest, err.Error())
		} else {
			s.fail(w, r, http.StatusBadRequest, "Camera details and location must each be shown or hidden.")
		}
		return false
	}
	return true
}

// uploadSharing resolves both choices for an upload. Anonymous uploads are
// public and share neither. Otherwise each follows DefaultShown unless the
// uploader chose.
func uploadSharing(r *http.Request, owner *models.User, visibility models.Visibility) (camera, location bool) {
	if owner == nil {
		return false, false
	}
	camera, location = models.DefaultShown(visibility), models.DefaultShown(visibility)
	chosenCamera, chosenLocation, err := (&params{form: r.Form}).sharingChoices()
	if err != nil {
		return false, false
	}
	if chosenCamera != nil {
		camera = *chosenCamera
	}
	if chosenLocation != nil {
		location = *chosenLocation
	}
	return camera, location
}

// errAlbumSharing is returned when a client still sends the album-level
// settings that were folded into each file.
var errAlbumSharing = errors.New("albums no longer have metadata or location settings; set them on each file")

func (p *params) refuseAlbumSharing() error {
	for _, key := range []string{cameraField, locationField} {
		if _, ok := p.json[key]; ok {
			return errAlbumSharing
		}
		if _, ok := p.form[key]; ok {
			return errAlbumSharing
		}
	}
	return nil
}

// handleFileSharing sets one of a file's sharing choices from a form.
func (s *Server) handleFileSharing(w http.ResponseWriter, r *http.Request, key string) {
	file, ok := s.loadChangeableFile(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "That form couldn't be read. Please try again.")
		return
	}
	value, err := (&params{form: r.Form}).shown(key)
	if err != nil || value == nil {
		s.fail(w, r, http.StatusBadRequest, "Choose whether to show or hide this.")
		return
	}
	update := store.FileUpdate{ShowCamera: value}
	notice := "Camera details are now hidden."
	if *value {
		notice = "Camera details are now shown."
	}
	if key == locationField {
		update = store.FileUpdate{ShowLocation: value}
		notice = "Location is now hidden."
		if *value {
			notice = "Location is now shown."
		}
	}
	if err := s.store.UpdateFile(r.Context(), file.ID, update); err != nil {
		s.log.Error("set sharing choice", "id", file.ID, "field", key, "error", err)
		s.fail(w, r, http.StatusInternalServerError, "That change couldn't be saved. Please try again.")
		return
	}
	// The details panel and map change with either choice.
	if isHTMX(r) {
		hxRedirect(w, "/f/"+file.ID)
		return
	}
	s.redirectFlash(w, r, "/f/"+file.ID, flashNotice, notice)
}

func (s *Server) handleFileMetadata(w http.ResponseWriter, r *http.Request) {
	s.handleFileSharing(w, r, cameraField)
}

func (s *Server) handleFileLocation(w http.ResponseWriter, r *http.Request) {
	s.handleFileSharing(w, r, locationField)
}
