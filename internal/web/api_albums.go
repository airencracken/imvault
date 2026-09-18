// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"imvault/internal/models"
	"imvault/internal/store"
)

// apiAlbumJSON is the wire representation of an album.
type apiAlbumJSON struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Public      bool   `json:"public"`
	FileCount   int    `json:"file_count"`
	CreatedAt   string `json:"created_at"`
	PageURL     string `json:"page_url"`
}

// apiAlbumDetailJSON adds the album's members.
type apiAlbumDetailJSON struct {
	apiAlbumJSON
	Files []apiFileJSON `json:"files"`
}

func newAPIAlbum(r *http.Request, s *Server, a *models.Album) apiAlbumJSON {
	return apiAlbumJSON{
		ID:          a.ID,
		Title:       a.Title,
		Slug:        a.Slug,
		Description: a.Description,
		Public:      a.IsPublic,
		FileCount:   a.FileCount,
		CreatedAt:   a.CreatedAt.UTC().Format(time.RFC3339),
		PageURL:     s.absoluteURL(r, "/a/"+a.Slug),
	}
}

// apiListAlbums returns the caller's albums, newest first.
func (s *Server) apiListAlbums(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	albums, err := s.store.AlbumsByUser(r.Context(), user.ID)
	if err != nil {
		s.log.Error("api: list albums", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "database error")
		return
	}

	out := make([]apiAlbumJSON, 0, len(albums))
	for _, a := range albums {
		out = append(out, newAPIAlbum(r, s, a))
	}

	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"albums": out,
		"total":  len(out),
	})
}

// apiCreateAlbum creates an album. The title is the only required field.
func (s *Server) apiCreateAlbum(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	params, err := newParams(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	title := params.str("title")
	if title == "" {
		writeAPIError(w, http.StatusBadRequest, "a title is required")
		return
	}

	public, _ := params.boolPtr("public")

	album, err := s.store.CreateAlbum(r.Context(), user.ID, title, params.str("description"), public)
	if err != nil {
		s.log.Error("api: create album", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "could not create the album")
		return
	}

	// Optionally seed the album with existing files in the same call.
	added := 0
	if refs := params.strs("files"); len(refs) > 0 {
		added = s.apiAttachFiles(r, user, album, refs)
	}
	if added > 0 {
		if refreshed, err := s.store.AlbumByID(r.Context(), album.ID); err == nil {
			album = refreshed
		}
	}

	noStore(w)
	writeJSON(w, http.StatusCreated, newAPIAlbum(r, s, album))
}

// apiGetAlbum returns an album and its members.
func (s *Server) apiGetAlbum(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	album, err := s.ownedAlbum(r.Context(), user, r.PathValue("ref"))
	if err != nil {
		s.writeAlbumLookupError(w, err)
		return
	}

	albumID := album.ID
	files, err := s.store.ListFiles(r.Context(), store.FileQuery{
		AlbumID: &albumID,
		Limit:   500,
	})
	if err != nil {
		s.log.Error("api: album files", "album", album.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "database error")
		return
	}

	out := make([]apiFileJSON, 0, len(files))
	for _, f := range files {
		out = append(out, newAPIFile(r, s, f))
	}

	noStore(w)
	writeJSON(w, http.StatusOK, apiAlbumDetailJSON{
		apiAlbumJSON: newAPIAlbum(r, s, album),
		Files:        out,
	})
}

// apiPatchAlbum updates the mutable album fields. Omitted fields are left
// alone, so a client can flip one flag without restating the rest.
func (s *Server) apiPatchAlbum(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	album, err := s.ownedAlbum(r.Context(), user, r.PathValue("ref"))
	if err != nil {
		s.writeAlbumLookupError(w, err)
		return
	}

	params, err := newParams(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	title := album.Title
	if provided := params.str("title"); provided != "" {
		title = provided
	}
	description := album.Description
	if provided := params.str("description"); provided != "" {
		description = provided
	}
	public := album.IsPublic
	if provided, present := params.boolPtr("public"); present {
		public = provided
	}

	if err := s.store.UpdateAlbum(r.Context(), album.ID, title, description, public); err != nil {
		s.log.Error("api: update album", "album", album.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "could not update the album")
		return
	}

	updated, err := s.store.AlbumByID(r.Context(), album.ID)
	if err != nil {
		s.log.Error("api: reload album", "album", album.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "could not reload the album")
		return
	}

	noStore(w)
	writeJSON(w, http.StatusOK, newAPIAlbum(r, s, updated))
}

// apiDeleteAlbum removes an album, leaving its files in place.
func (s *Server) apiDeleteAlbum(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	album, err := s.ownedAlbum(r.Context(), user, r.PathValue("ref"))
	if err != nil {
		s.writeAlbumLookupError(w, err)
		return
	}

	if err := s.store.DeleteAlbum(r.Context(), album.ID); err != nil {
		s.log.Error("api: delete album", "album", album.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "could not delete the album")
		return
	}

	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "deleted",
		"id":     album.ID,
		"slug":   album.Slug,
	})
}

// apiAddAlbumFiles links files into an album.
func (s *Server) apiAddAlbumFiles(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	album, err := s.ownedAlbum(r.Context(), user, r.PathValue("ref"))
	if err != nil {
		s.writeAlbumLookupError(w, err)
		return
	}

	params, err := newParams(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	refs := params.strs("files")
	if len(refs) == 0 {
		writeAPIError(w, http.StatusBadRequest, "no files were provided")
		return
	}

	added := s.apiAttachFiles(r, user, album, refs)

	updated, err := s.store.AlbumByID(r.Context(), album.ID)
	if err != nil {
		updated = album
	}

	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"album": newAPIAlbum(r, s, updated),
		"added": added,
	})
}

// apiRemoveAlbumFile unlinks one file from an album.
func (s *Server) apiRemoveAlbumFile(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	album, err := s.ownedAlbum(r.Context(), user, r.PathValue("ref"))
	if err != nil {
		s.writeAlbumLookupError(w, err)
		return
	}

	fileID := r.PathValue("fileID")
	if err := s.store.RemoveFileFromAlbum(r.Context(), album.ID, fileID); err != nil {
		s.log.Error("api: remove file from album", "album", album.ID, "file", fileID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "could not update the album")
		return
	}

	updated, err := s.store.AlbumByID(r.Context(), album.ID)
	if err != nil {
		updated = album
	}

	noStore(w)
	writeJSON(w, http.StatusOK, newAPIAlbum(r, s, updated))
}

// apiAttachFiles links the referenced files the caller actually owns, skipping
// anything missing or belonging to somebody else. It returns how many were
// newly added.
func (s *Server) apiAttachFiles(r *http.Request, user *models.User, album *models.Album, refs []string) int {
	ctx := r.Context()
	added := 0

	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}

		file, err := s.store.FileByID(ctx, ref)
		if err != nil {
			s.log.Warn("api: album add skipped unknown file", "album", album.Slug, "file", ref)
			continue
		}
		if !canEditFile(user, file) {
			s.log.Warn("api: album add skipped unowned file", "album", album.Slug, "file", ref)
			continue
		}
		if err := s.store.AddFileToAlbum(ctx, album.ID, file.ID); err != nil {
			s.log.Error("api: add file to album", "album", album.ID, "file", file.ID, "error", err)
			continue
		}
		added++
	}

	return added
}

// writeAlbumLookupError reports a missing or unowned album identically.
func (s *Server) writeAlbumLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "no such album")
		return
	}
	s.log.Error("api: load album", "error", err)
	writeAPIError(w, http.StatusInternalServerError, "database error")
}
