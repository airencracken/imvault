// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"github.com/airencracken/comfylib/brandimage"
	"io"
	"net/http"

	"imvault/internal/store"
)

const maxBrandImageBytes = brandimage.MaxBytes

func (s *Server) handleBrandingAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name != "mascot" && name != "favicon" {
		http.NotFound(w, r)
		return
	}
	content, err := s.store.BrandingAsset(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.log.Error("load branding asset", "name", name, "error", err)
		http.Error(w, "could not load image", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	s.writeBody(w, content)
}

func (s *Server) handleAdminSaveBrandingAssets(w http.ResponseWriter, r *http.Request) {
	mascot, err := uploadedBrandImage(r, "mascot")
	if err != nil {
		s.redirectFlash(w, r, "/admin/settings", flashError, err.Error())
		return
	}
	favicon, err := uploadedBrandImage(r, "favicon")
	if err != nil {
		s.redirectFlash(w, r, "/admin/settings", flashError, err.Error())
		return
	}
	removeMascot := r.FormValue("remove_mascot") == "1"
	removeFavicon := r.FormValue("remove_favicon") == "1"
	if len(mascot) == 0 && len(favicon) == 0 && !removeMascot && !removeFavicon {
		s.redirectFlash(w, r, "/admin/settings", flashError, "Choose an image or select one to remove.")
		return
	}
	if err := s.store.SaveBrandingAssets(r.Context(), mascot, favicon, removeMascot, removeFavicon); err != nil {
		s.log.Error("admin: save branding assets", "error", err)
		s.redirectFlash(w, r, "/admin/settings", flashError, "Could not save the images.")
		return
	}
	if err := s.reloadSettings(r.Context()); err != nil {
		s.log.Error("admin: reload after branding assets", "error", err)
		s.redirectFlash(w, r, "/admin/settings", flashError, "The images were saved but could not be read back.")
		return
	}
	s.redirectFlash(w, r, "/admin/settings", flashNotice, "Brand images saved.")
}

func uploadedBrandImage(r *http.Request, field string) ([]byte, error) {
	file, header, err := r.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		// The upload is a temporary multipart file; nothing depends on
		// closing it beyond releasing the handle.
		_ = file.Close()
	}()
	if header.Size > maxBrandImageBytes {
		return nil, errors.New("choose an image no larger than 2 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBrandImageBytes+1))
	if err != nil {
		return nil, err
	}
	return brandimage.Normalize(data)
}
