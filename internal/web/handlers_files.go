// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"imvault/internal/models"
	"imvault/internal/storage"
	"imvault/internal/store"
)

// handleFileRaw streams the original upload, supporting range requests.
func (s *Server) handleFileRaw(w http.ResponseWriter, r *http.Request) {
	file, ok := s.lookupVisibleFile(w, r)
	if !ok {
		return
	}

	// Count the view once we know the request is legitimate.
	if err := s.store.IncrementViews(r.Context(), file.ID); err != nil {
		s.log.Error("increment views", "id", file.ID, "error", err)
	}

	disposition := mime.FormatMediaType("inline", map[string]string{"filename": file.OriginalName})
	s.serveObject(w, r, file, file.ObjectKey, file.Mime, disposition)
}

// handleFileThumb streams the generated thumbnail.
func (s *Server) handleFileThumb(w http.ResponseWriter, r *http.Request) {
	file, ok := s.lookupVisibleFile(w, r)
	if !ok {
		return
	}
	s.serveObject(w, r, file, file.ThumbKey, mimeForKey(file.ThumbKey), "inline")
}

// handleFilePreview streams the preview. Animations and clips have no separate
// preview rendition, so this falls back to the stored original, which is what
// the browser should play.
func (s *Server) handleFilePreview(w http.ResponseWriter, r *http.Request) {
	file, ok := s.lookupVisibleFile(w, r)
	if !ok {
		return
	}

	key := file.PreviewKeyOrObject()
	contentType := file.PreviewMime()
	if contentType == "" {
		contentType = mimeForKey(key)
	}
	s.serveObject(w, r, file, key, contentType, "inline")
}

// serveObject writes a stored object with HTTP caching metadata.
func (s *Server) serveObject(w http.ResponseWriter, r *http.Request, file *models.File, key, contentType, disposition string) {
	if key == "" {
		http.NotFound(w, r)
		return
	}

	f, err := s.objects.Open(key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.log.Error("object missing from storage", "id", file.ID, "key", key)
			http.NotFound(w, r)
			return
		}
		s.log.Error("open object", "id", file.ID, "key", key, "error", err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", `"`+file.ID+`-`+path.Base(key)+`"`)

	// A given id's bytes never change, so the content is safe to cache for a
	// long time. Anything not public is marked private to keep shared caches
	// out, since a members-only file is still a signed-in-only file.
	scope := "private"
	if file.Visibility.IsPublic() {
		scope = "public"
	}
	w.Header().Set("Cache-Control", scope+", max-age=31536000, immutable")

	http.ServeContent(w, r, file.OriginalName, time.Time{}, f)
}

// handleFileVisibility changes which level a file is visible at.
func (s *Server) handleFileVisibility(w http.ResponseWriter, r *http.Request) {
	file, ok := s.loadEditableFile(w, r)
	if !ok {
		return
	}

	visibility := models.ParseVisibility(r.FormValue("visibility"))
	if err := s.store.SetFileVisibility(r.Context(), file.ID, visibility); err != nil {
		s.log.Error("set visibility", "id", file.ID, "error", err)
		http.Error(w, "could not update visibility", http.StatusInternalServerError)
		return
	}
	file.Visibility = visibility

	if isHTMX(r) {
		s.renderPartial(w, "visibility_button", fileView{
			base: s.base(r, file.OriginalName),
			File: file,
		})
		return
	}
	redirectNotice(w, r, "/f/"+file.ID, "notice", "Visibility updated.")
}

// handleFileDelete removes a file and its stored objects.
func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	file, ok := s.loadEditableFile(w, r)
	if !ok {
		return
	}

	// Drop the database row first: if that fails nothing is lost and the
	// object can be retried, whereas orphaned bytes are recoverable.
	if err := s.deleteFileAndRelease(r.Context(), file); err != nil {
		s.log.Error("delete file", "id", file.ID, "error", err)
		http.Error(w, "could not delete the image", http.StatusInternalServerError)
		return
	}

	next := safeNext(r.FormValue("next"))
	switch {
	case next != "":
		hxRedirect(w, next)
	case isHTMX(r):
		// Empty body with an outerHTML swap removes the card from the grid.
		w.WriteHeader(http.StatusOK)
	default:
		redirectNotice(w, r, "/gallery", "notice", "Image deleted.")
	}
}

// handleTagAdd attaches a tag to a file.
func (s *Server) handleTagAdd(w http.ResponseWriter, r *http.Request) {
	file, ok := s.loadEditableFile(w, r)
	if !ok {
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.renderTagsFragment(w, r, file)
		return
	}

	if _, err := s.store.AddTag(r.Context(), file.ID, tagOwner(file), name); err != nil {
		s.log.Error("add tag", "id", file.ID, "error", err)
		http.Error(w, "could not add the tag", http.StatusInternalServerError)
		return
	}
	s.renderTagsFragment(w, r, file)
}

// handleTagRemove detaches a tag from a file.
func (s *Server) handleTagRemove(w http.ResponseWriter, r *http.Request) {
	file, ok := s.loadEditableFile(w, r)
	if !ok {
		return
	}

	tagID, err := strconv.ParseInt(r.PathValue("tagID"), 10, 64)
	if err != nil {
		http.Error(w, "invalid tag", http.StatusBadRequest)
		return
	}

	if err := s.store.RemoveTag(r.Context(), file.ID, tagID); err != nil {
		s.log.Error("remove tag", "id", file.ID, "tag", tagID, "error", err)
		http.Error(w, "could not remove the tag", http.StatusInternalServerError)
		return
	}
	s.renderTagsFragment(w, r, file)
}

func (s *Server) renderTagsFragment(w http.ResponseWriter, r *http.Request, file *models.File) {
	tags, err := s.store.TagsForFile(r.Context(), file.ID)
	if err != nil {
		s.log.Error("load tags", "id", file.ID, "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, "tag_chips", tagsFragmentView{
		base:    s.base(r, file.OriginalName),
		File:    file,
		Tags:    tags,
		CanEdit: canEditFile(currentUser(r.Context()), file),
	})
}

// tagOwner returns the namespace a file's tags belong to.
//
// Tags live with the file rather than with the person doing the tagging, so an
// administrator editing somebody else's upload does not leave their own labels
// on it. An anonymous upload has no account, so its tags go to the shared
// namespace: nil.
func tagOwner(file *models.File) *int64 {
	return file.UserID
}

// loadEditableFile resolves {id} and enforces ownership.
func (s *Server) loadEditableFile(w http.ResponseWriter, r *http.Request) (*models.File, bool) {
	id := r.PathValue("id")

	file, err := s.store.FileByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return nil, false
		}
		s.log.Error("load file", "id", id, "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return nil, false
	}

	if !canEditFile(currentUser(r.Context()), file) {
		http.Error(w, "not permitted", http.StatusForbidden)
		return nil, false
	}
	return file, true
}

// mimeForKey infers a content type from a stored object's extension.
func mimeForKey(key string) string {
	switch strings.ToLower(path.Ext(key)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".tiff", ".tif":
		return "image/tiff"
	default:
		return "application/octet-stream"
	}
}
