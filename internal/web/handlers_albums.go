// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"imvault/internal/models"
	"imvault/internal/store"
)

// albumFileLimit caps how many candidates the "add files" picker shows.
const albumFileLimit = 120

// handleAlbumsPage lists the signed-in user's albums.
func (s *Server) handleAlbumsPage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	albums, err := s.store.AlbumsByUser(r.Context(), user.ID)
	if err != nil {
		s.log.Error("albums page: list", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	s.renderPage(w, http.StatusOK, "albums", albumsView{
		base:   s.base(r, "Albums"),
		Albums: albums,
	})
}

// handleAlbumCreate creates an album and navigates to it.
func (s *Server) handleAlbumCreate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		redirectNotice(w, r, "/albums", "error", "An album needs a title.")
		return
	}

	album, err := s.store.CreateAlbum(r.Context(),
		user.ID,
		title,
		strings.TrimSpace(r.FormValue("description")),
		r.FormValue("public") == "1",
	)
	if err != nil {
		s.log.Error("create album", "error", err)
		redirectNotice(w, r, "/albums", "error", "Could not create the album.")
		return
	}

	if isHTMX(r) {
		hxRedirect(w, "/a/"+album.Slug)
		return
	}
	http.Redirect(w, r, "/a/"+album.Slug, http.StatusSeeOther)
}

// handleAlbumPage shows an album to its owner or, when public, to anyone.
func (s *Server) handleAlbumPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	album, err := s.store.AlbumBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r, "No album by that name.")
			return
		}
		s.log.Error("album page: lookup", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	user := currentUser(r.Context())
	isOwner := user != nil && (user.IsAdmin || user.ID == album.UserID)
	if !album.IsPublic && !isOwner {
		s.notFound(w, r, "No album by that name.")
		return
	}

	albumID := album.ID
	filter := store.FileQuery{AlbumID: &albumID}
	if isOwner {
		filter.VisibleTo = &user.ID
	} else {
		filter.PublicOnly = true
	}

	files, err := s.store.ListFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("album page: list files", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	view := albumView{
		base:    s.base(r, album.Title),
		Album:   album,
		IsOwner: isOwner,
		Grid: s.grid(r, files, isOwner, false,
			"/a/"+album.Slug+"/files/%s/delete",
			"This album is empty."),
	}

	if isOwner {
		available, err := s.albumCandidates(r, user, album.ID)
		if err != nil {
			s.log.Error("album page: candidates", "error", err)
		}
		view.Available = s.grid(r, available, false, true, "",
			"Every image in your gallery is already in this album.")
	}

	s.renderPage(w, http.StatusOK, "album", view)
}

// albumCandidates lists the owner's files that are not yet in the album.
func (s *Server) albumCandidates(r *http.Request, user *models.User, albumID int64) ([]*models.File, error) {
	members, err := s.store.AlbumMembership(r.Context(), albumID)
	if err != nil {
		return nil, err
	}

	files, err := s.store.ListFiles(r.Context(), store.FileQuery{
		OwnerID: &user.ID,
		Limit:   albumFileLimit,
	})
	if err != nil {
		return nil, err
	}

	out := files[:0]
	for _, f := range files {
		if !members[f.ID] {
			out = append(out, f)
		}
	}
	return out, nil
}

// handleAlbumDelete removes an album, leaving its files in place.
func (s *Server) handleAlbumDelete(w http.ResponseWriter, r *http.Request) {
	album, ok := s.loadEditableAlbum(w, r)
	if !ok {
		return
	}

	if err := s.store.DeleteAlbum(r.Context(), album.ID); err != nil {
		s.log.Error("delete album", "id", album.ID, "error", err)
		http.Error(w, "could not delete the album", http.StatusInternalServerError)
		return
	}

	if isHTMX(r) {
		hxRedirect(w, "/albums")
		return
	}
	redirectNotice(w, r, "/albums", "notice", "Album deleted.")
}

// handleAlbumAddFiles links selected files into the album.
func (s *Server) handleAlbumAddFiles(w http.ResponseWriter, r *http.Request) {
	album, ok := s.loadEditableAlbum(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	user := currentUser(r.Context())
	added := 0

	for _, fileID := range r.Form["files"] {
		file, err := s.store.FileByID(r.Context(), fileID)
		if err != nil {
			continue // vanished or bogus id
		}
		if !canEditFile(user, file) {
			continue // never let one user file another's uploads
		}
		if err := s.store.AddFileToAlbum(r.Context(), album.ID, file.ID); err != nil {
			s.log.Error("add file to album", "album", album.ID, "file", file.ID, "error", err)
			continue
		}
		added++
	}

	if isHTMX(r) {
		hxRedirect(w, "/a/"+album.Slug)
		return
	}
	redirectNotice(w, r, "/a/"+album.Slug, "notice", "Album updated.")
}

// handleAlbumRemoveFile unlinks a file from the album.
func (s *Server) handleAlbumRemoveFile(w http.ResponseWriter, r *http.Request) {
	album, ok := s.loadEditableAlbum(w, r)
	if !ok {
		return
	}

	fileID := r.PathValue("fileID")
	if err := s.store.RemoveFileFromAlbum(r.Context(), album.ID, fileID); err != nil {
		s.log.Error("remove file from album", "album", album.ID, "file", fileID, "error", err)
		http.Error(w, "could not update the album", http.StatusInternalServerError)
		return
	}

	if isHTMX(r) {
		hxRedirect(w, "/a/"+album.Slug)
		return
	}
	redirectNotice(w, r, "/a/"+album.Slug, "notice", "Removed from album.")
}

// ownedAlbum resolves an album reference — a numeric id or a slug — and
// confirms the user may administer it. A missing album and one owned by
// somebody else are reported identically, so the API cannot be used to probe
// for other people's albums.
func (s *Server) ownedAlbum(ctx context.Context, user *models.User, ref string) (*models.Album, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, store.ErrNotFound
	}

	var (
		album *models.Album
		err   error
	)
	if id, convErr := strconv.ParseInt(ref, 10, 64); convErr == nil {
		album, err = s.store.AlbumByID(ctx, id)
	} else {
		album, err = s.store.AlbumBySlug(ctx, ref)
	}
	if err != nil {
		return nil, err
	}

	if album.UserID != user.ID && !user.IsAdmin {
		return nil, store.ErrNotFound
	}
	return album, nil
}

// loadEditableAlbum resolves {slug} and enforces ownership.
func (s *Server) loadEditableAlbum(w http.ResponseWriter, r *http.Request) (*models.Album, bool) {
	album, err := s.store.AlbumBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return nil, false
		}
		s.log.Error("load album", "slug", r.PathValue("slug"), "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return nil, false
	}

	user := currentUser(r.Context())
	if user == nil || (!user.IsAdmin && user.ID != album.UserID) {
		http.Error(w, "not permitted", http.StatusForbidden)
		return nil, false
	}
	return album, true
}
