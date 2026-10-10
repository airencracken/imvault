// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/airencracken/comfylib/brandimage"
	"imvault/internal/store"
)

func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	picture, err := s.store.Avatar(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.avatarError(w, err)
		return
	}
	on, err := s.store.AnimateAvatars(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		s.avatarError(w, err)
		return
	}
	content, kind := picture.Still, "image/png"
	if on && r.URL.Query().Get("still") != "1" && len(picture.Animation) > 0 {
		content, kind = picture.Animation, "image/gif"
	}
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(content))
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	s.writeBody(w, content)
}

func (s *Server) avatarError(w http.ResponseWriter, err error) {
	s.log.Error("avatar operation", "error", err)
	http.Error(w, "Could not update or load the avatar.", 500)
}

func avatarUpload(r *http.Request) ([]byte, bool, error) {
	values := r.PostForm["remove"]
	if len(values) > 1 || len(values) == 1 && values[0] != "1" {
		return nil, false, errors.New("Choose a valid removal option.")
	}
	remove := len(values) == 1
	var count int
	if r.MultipartForm != nil {
		for field, files := range r.MultipartForm.File {
			if field != "avatar" {
				return nil, false, errors.New("Choose one avatar image.")
			}
			count += len(files)
		}
	}
	if remove && count == 0 {
		return nil, true, nil
	}
	if remove || count != 1 {
		return nil, false, errors.New("Choose one image, or remove your avatar.")
	}
	file, header, err := r.FormFile("avatar")
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	if header.Size > brandimage.MaxBytes {
		return nil, false, errors.New("Choose an image no larger than 2 MiB.")
	}
	data, err := io.ReadAll(io.LimitReader(file, brandimage.MaxBytes+1))
	return data, false, err
}

func (s *Server) handleAvatarSave(w http.ResponseWriter, r *http.Request) {
	data, remove, err := avatarUpload(r)
	if err != nil {
		s.redirectFlash(w, r, "/settings/profile#avatar", flashError, err.Error())
		return
	}
	user := currentUser(r.Context())
	if remove {
		err = s.store.DeleteAvatar(r.Context(), user.ID)
	} else {
		release, ok := s.acquireUpload(w, r)
		if !ok {
			return
		}
		defer release()
		err = s.store.SaveAvatar(r.Context(), user.ID, data)
	}
	if err != nil {
		s.redirectFlash(w, r, "/settings/profile#avatar", flashError, "Choose a valid PNG, JPEG, or GIF up to 512 by 512 pixels and 2 MiB, with at most 64 GIF frames.")
		return
	}
	s.redirectFlash(w, r, "/settings/profile#avatar", flashNotice, "Avatar saved.")
}

func (s *Server) handleAvatarPreference(w http.ResponseWriter, r *http.Request) {
	values := r.PostForm["animate"]
	if len(values) > 1 || len(values) == 1 && values[0] != "1" {
		http.Error(w, "Choose a valid animation preference.", 422)
		return
	}
	if err := s.store.SetAnimateAvatars(r.Context(), currentUser(r.Context()).ID, len(values) == 1); err != nil {
		s.avatarError(w, err)
		return
	}
	s.redirectFlash(w, r, "/settings/profile#avatar", flashNotice, "Animation preference saved.")
}

func (s *Server) handleAdminAvatarRemove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err = s.store.DeleteAvatar(r.Context(), id); err != nil {
		s.avatarError(w, err)
		return
	}
	s.redirectFlash(w, r, fmt.Sprintf("/members/%d", id), flashNotice, "Avatar removed.")
}
