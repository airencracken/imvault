// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"crypto/sha256"
	"errors"
	"fmt"
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

	key, ok := s.objectFor(w, r, file, file.ObjectKey)
	if !ok {
		return
	}

	disposition := mime.FormatMediaType("inline", map[string]string{"filename": file.OriginalName})
	s.serveObject(w, r, file, key, file.Mime, disposition)
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
	// A rendition has already been re-encoded and carries nothing, so the
	// policy only has anything to say when the preview is the stored object,
	// which is how animations and clips are served.
	if key == file.ObjectKey {
		resolved, ok := s.objectFor(w, r, file, key)
		if !ok {
			return
		}
		key = resolved
	}

	contentType := file.PreviewMime()
	if contentType == "" {
		contentType = mimeForKey(key)
	}
	s.serveObject(w, r, file, key, contentType, "inline")
}

// serveObject writes a stored object with HTTP caching metadata.
// objectFor resolves which stored object should be sent, writing the refusal
// itself when the metadata cannot be removed.
func (s *Server) objectFor(w http.ResponseWriter, r *http.Request, file *models.File, original string) (string, bool) {
	key, err := s.scopedObjectKey(r.Context(), file, original)
	if err != nil {
		s.metadataUnavailable(w, r, file, err)
		return "", false
	}
	return key, true
}

// metadataUnavailable reports content whose metadata should have been removed
// and could not be.
//
// It refuses rather than serving the original, because serving it would be
// indistinguishable from success — the file would look clean and the
// coordinates would be in it. Refusing is also not a dead end: the owner can
// set the file's metadata to Shown, which is an explicit decision to accept the
// exposure, and until they do the interface says what is wrong.
func (s *Server) metadataUnavailable(w http.ResponseWriter, r *http.Request, file *models.File, err error) {
	s.log.Warn("metadata could not be removed, so the file is not being served",
		"id", file.ID, "ext", file.Ext, "error", err)

	message := "This file is set to hide its metadata, but it could not be removed. " +
		"It is not being served. Set its metadata to \"Shown\" to accept the exposure, " +
		"or upload it as JPEG, PNG, WebP, GIF, or BMP."
	if gap := s.MetadataGap(file); gap != "" {
		message = "This file is set to hide its metadata, but it could not be removed. " + gap +
			" It is not being served. Set its metadata to \"Shown\" to accept the exposure."
	}

	s.renderPage(w, http.StatusBadGateway, "notfound", errorView{
		base:    s.base(r, "Metadata could not be removed"),
		Code:    "Not served",
		Message: message,
	})
}

func (s *Server) serveObject(w http.ResponseWriter, r *http.Request, file *models.File, key, contentType, disposition string) {
	if key == "" {
		http.NotFound(w, r)
		return
	}

	f, err := s.objects.Open(r.Context(), key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.log.Error("object missing from storage", "id", file.ID, "key", key)
			// A metadata-free copy that has gone missing is rebuilt on the next
			// request instead of failing this file for good. The original is
			// not a substitute for it, which is why this forgets rather than
			// falls back.
			if key != file.ObjectKey {
				s.forgetCleanObject(r.Context(), file.SHA256)
			}
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
	etag := file.ID + "-" + path.Base(key)

	// A given id's bytes never change, so the content is safe to cache for a
	// long time. Anything not public is marked private to keep shared caches
	// out, since a members-only file is still a signed-in-only file.
	scope := "private"
	if file.Visibility.IsPublic() {
		scope = "public"
	}
	cacheControl := scope + ", max-age=31536000, immutable"
	if disposition != "inline" {
		// Original downloads must revalidate their filename after a rename.
		// Include the disposition so an old validator cannot preserve it.
		nameHash := sha256.Sum256([]byte(disposition))
		etag += fmt.Sprintf("-%x", nameHash[:8])
		cacheControl = scope + ", no-cache"
	}
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Cache-Control", cacheControl)

	http.ServeContent(w, r, file.OriginalName, time.Time{}, f)
}

// handleFileVisibility changes which level a file is visible at.
func (s *Server) handleFileVisibility(w http.ResponseWriter, r *http.Request) {
	file, ok := s.loadChangeableFile(w, r)
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

// handleFileMetadata changes what happens to a file's metadata.
func (s *Server) handleFileMetadata(w http.ResponseWriter, r *http.Request) {
	file, ok := s.loadChangeableFile(w, r)
	if !ok {
		return
	}

	policy := models.ParseMetadataPolicy(r.FormValue("metadata"))
	if err := s.store.SetFileMetadata(r.Context(), file.ID, policy); err != nil {
		s.log.Error("set metadata policy", "id", file.ID, "error", err)
		http.Error(w, "could not update the metadata setting", http.StatusInternalServerError)
		return
	}
	file.Metadata = policy

	if isHTMX(r) {
		s.renderPartial(w, "metadata_button", fileView{
			base: s.base(r, file.OriginalName),
			File: file,
		})
		return
	}
	redirectNotice(w, r, "/f/"+file.ID, "notice", "Metadata setting updated.")
}

// handleFileDelete removes a file and its stored objects.
func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	file, ok := s.loadDeletableFile(w, r)
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
	s.recordFileRemoval(r.Context(), currentUser(r.Context()), file, "")

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
	file, ok := s.loadChangeableFile(w, r)
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
	file, ok := s.loadChangeableFile(w, r)
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
		CanEdit: canChangeFile(currentUser(r.Context()), file),
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

// loadFile resolves {id} to a file, or writes the error response itself.
func (s *Server) loadFile(w http.ResponseWriter, r *http.Request) (*models.File, bool) {
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
	return file, true
}

// loadChangeableFile enforces the right to change what a file *is*. A moderator
// may remove content but not republish it, so this is narrower than deletion.
func (s *Server) loadChangeableFile(w http.ResponseWriter, r *http.Request) (*models.File, bool) {
	file, ok := s.loadFile(w, r)
	if !ok {
		return nil, false
	}
	if !canChangeFile(currentUser(r.Context()), file) {
		http.Error(w, "not permitted", http.StatusForbidden)
		return nil, false
	}
	return file, true
}

// loadDeletableFile enforces the right to remove a file, which a moderator has
// for anybody's content.
func (s *Server) loadDeletableFile(w http.ResponseWriter, r *http.Request) (*models.File, bool) {
	file, ok := s.loadFile(w, r)
	if !ok {
		return nil, false
	}
	if !canDeleteFile(currentUser(r.Context()), file) {
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
