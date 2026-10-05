// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"imvault/internal/imaging"
	"imvault/internal/models"
)

func (s *Server) handleFileRotate(w http.ResponseWriter, r *http.Request) {
	file, ok := s.loadChangeableFile(w, r)
	if !ok {
		return
	}
	if !file.CanRotate() {
		http.Error(w, "Only still photos with previews can be rotated.", http.StatusUnprocessableEntity)
		return
	}
	direction := r.PostForm.Get("direction")
	if direction != "left" && direction != "right" && direction != "reset" {
		http.Error(w, "Choose rotate left, rotate right, or reset.", http.StatusBadRequest)
		return
	}
	if err := s.store.RotateFile(r.Context(), file.ID, direction); err != nil {
		s.log.Error("rotate photo", "id", file.ID, "error", err)
		http.Error(w, "Could not rotate this photo.", http.StatusInternalServerError)
		return
	}
	s.redirectFlash(w, r, "/f/"+file.ID, flashNotice, "Photo rotation saved.")
}

func (s *Server) handleRotatedDownload(w http.ResponseWriter, r *http.Request) {
	file, ok := s.lookupVisibleFile(w, r)
	if !ok {
		return
	}
	if file.HasMotion() || file.Rotation == 0 {
		http.NotFound(w, r)
		return
	}
	s.serveRotatedPhoto(w, r, file, file.ObjectKey, true)
}

// Rotations are derived when requested and cached by the browser. The source
// objects stay immutable and backups need only the existing objects and the
// rotation stored in SQLite. Decodes share the upload processing budget.
func (s *Server) serveRotatedPhoto(w http.ResponseWriter, r *http.Request, file *models.File, key string, download bool) {
	if key == "" || file.HasMotion() {
		http.NotFound(w, r)
		return
	}
	etag, cache := rotationHeaders(file, key, download, r.URL.Query().Get("v") != "")
	w.Header().Set("Cache-Control", "no-store")
	if rotationNotModified(w, r, etag, cache) {
		return
	}
	if download {
		finish, ok := s.exports.acquire(r.Context())
		if !ok {
			w.Header().Set("Retry-After", "10")
			http.Error(w, "The server is preparing another download. Try again in a moment.", http.StatusServiceUnavailable)
			return
		}
		defer finish()
	}
	release, ok := s.processing.acquire(r.Context())
	if !ok {
		w.Header().Set("Retry-After", "10")
		http.Error(w, "The server is busy processing photos. Try again in a moment.", http.StatusServiceUnavailable)
		return
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	source, err := s.objects.Open(r.Context(), key)
	if err != nil {
		s.log.Error("open photo for rotation", "id", file.ID, "error", err)
		http.Error(w, "Photo could not be opened.", http.StatusInternalServerError)
		return
	}
	defer s.closeLogged(source, "rotation source")
	rendition, err := imaging.Rotate(source, file.Rotation, 95, download)
	if err != nil {
		s.log.Error("render rotated photo", "id", file.ID, "error", err)
		http.Error(w, "Photo could not be rotated.", http.StatusInternalServerError)
		return
	}
	release()
	release = nil
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("Content-Type", rendition.Mime)
	name := file.OriginalName
	if download {
		name = strings.TrimSuffix(name, path.Ext(name)) + "-rotated.png"
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	} else {
		w.Header().Set("Content-Disposition", "inline")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(rendition.Data))
}

func rotationHeaders(file *models.File, key string, download, versioned bool) (string, string) {
	hash := sha256.Sum256([]byte(fmt.Sprintf("rotation-v1|%s|%d|%t", key, file.Rotation, download)))
	etag := fmt.Sprintf(`"%s-%x"`, file.ID, hash[:16])
	scope := "private"
	if file.Visibility.IsPublic() {
		scope = "public"
	}
	cache := scope + ", no-cache"
	if !download && versioned {
		cache = scope + ", max-age=31536000, immutable"
	}
	if download {
		nameHash := sha256.Sum256([]byte(file.OriginalName))
		etag = strings.TrimSuffix(etag, `"`) + fmt.Sprintf(`-%x"`, nameHash[:8])
	}
	return etag, cache
}

// Authorization runs before this shortcut, and only successful image responses
// carry the validator. Revalidating a cached photo need not decode pixels.
func rotationNotModified(w http.ResponseWriter, r *http.Request, etag, cache string) bool {
	for _, validator := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		validator = strings.TrimSpace(validator)
		if validator == "*" || strings.TrimPrefix(validator, "W/") == etag {
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", cache)
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	return false
}
