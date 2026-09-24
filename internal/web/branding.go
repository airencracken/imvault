// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"

	"imvault/internal/store"
)

const maxBrandImageBytes = 2 << 20

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
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(content)
}

func (s *Server) handleAdminSaveBrandingAssets(w http.ResponseWriter, r *http.Request) {
	mascot, err := uploadedBrandImage(r, "mascot")
	if err != nil {
		redirectNotice(w, r, "/admin/settings", "error", err.Error())
		return
	}
	favicon, err := uploadedBrandImage(r, "favicon")
	if err != nil {
		redirectNotice(w, r, "/admin/settings", "error", err.Error())
		return
	}
	removeMascot := r.FormValue("remove_mascot") == "1"
	removeFavicon := r.FormValue("remove_favicon") == "1"
	if len(mascot) == 0 && len(favicon) == 0 && !removeMascot && !removeFavicon {
		redirectNotice(w, r, "/admin/settings", "error", "Choose an image or select one to remove.")
		return
	}
	if err := s.store.SaveBrandingAssets(r.Context(), mascot, favicon, removeMascot, removeFavicon); err != nil {
		s.log.Error("admin: save branding assets", "error", err)
		redirectNotice(w, r, "/admin/settings", "error", "Could not save the images.")
		return
	}
	redirectNotice(w, r, "/admin/settings", "notice", "Brand images saved.")
}

func uploadedBrandImage(r *http.Request, field string) ([]byte, error) {
	file, header, err := r.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if header.Size > maxBrandImageBytes {
		return nil, errors.New("choose an image no larger than 2 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBrandImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxBrandImageBytes {
		return nil, errors.New("choose an image no larger than 2 MiB")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 || int64(config.Width)*int64(config.Height) > 4_194_304 {
		return nil, errors.New("choose a valid image up to 2048 by 2048 pixels")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("choose a PNG, JPEG, or GIF image")
	}
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
