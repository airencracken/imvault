// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"imvault/internal/models"
	"imvault/internal/store"
)

// albumFileLimit caps how many candidates the "add files" picker shows.
const albumFileLimit = 120

// handleAlbumsPage lists the signed-in user's albums, plus the ones somebody
// else made that they can see.
func (s *Server) handleAlbumsPage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	albums, err := s.store.AlbumsByUser(r.Context(), user.ID)
	if err != nil {
		s.log.Error("albums page: list", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	// Albums other people made that this account may see. Without this a
	// shared album would only be reachable by being handed its link, which is
	// not a collection a group can actually use.
	shared, err := s.store.AlbumsVisibleTo(r.Context(), user.ID)
	if err != nil {
		s.log.Error("albums page: visible to viewer", "error", err)
	}

	s.renderPage(w, http.StatusOK, "albums", albumsView{
		base:   s.base(r, "Albums"),
		Albums: albums,
		Shared: shared,
		Levels: models.VisibilityLevels(),
		Access: models.AlbumAccessLevels(),
	})
}

// albumVisibilityError reports combinations that cannot do anything.
//
// A shared album that only its owner can see is not shared with anybody, so it
// is refused rather than stored and left for the next person to wonder about.
func albumVisibilityError(visibility models.Visibility, access models.AlbumAccess) string {
	if access.Shared() && visibility == models.VisibilityPrivate {
		return "A shared album has to be visible to members, or nobody could find it."
	}
	return ""
}

// handleAlbumCreate creates an album and navigates to it.
func (s *Server) handleAlbumCreate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		redirectNotice(w, r, "/albums", "error", "An album needs a title.")
		return
	}

	visibility := models.ParseVisibility(r.FormValue("visibility"))
	access := models.ParseAlbumAccess(r.FormValue("access"))
	if message := albumVisibilityError(visibility, access); message != "" {
		redirectNotice(w, r, "/albums", "error", message)
		return
	}

	album, err := s.store.CreateAlbum(r.Context(),
		user.ID,
		title,
		strings.TrimSpace(r.FormValue("description")),
		visibility,
		access,
	)
	if err != nil {
		s.log.Error("create album", "error", err)
		redirectNotice(w, r, "/albums", "error", "Could not create the album.")
		return
	}

	if access.Shared() {
		s.log.Info("shared album created", "album", album.ID, "owner", user.ID)
	}

	if isHTMX(r) {
		hxRedirect(w, "/a/"+album.Slug)
		return
	}
	http.Redirect(w, r, "/a/"+album.Slug, http.StatusSeeOther)
}

// handleAlbumUpdate changes an album's title, description and levels. It is the
// owner's, and only the owner's, to change.
func (s *Server) handleAlbumUpdate(w http.ResponseWriter, r *http.Request) {
	album, ok := s.loadAdministerableAlbum(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		redirectNotice(w, r, "/a/"+album.Slug, "error", "An album needs a title.")
		return
	}

	visibility := models.ParseVisibility(r.FormValue("visibility"))
	access := models.ParseAlbumAccess(r.FormValue("access"))
	if message := albumVisibilityError(visibility, access); message != "" {
		redirectNotice(w, r, "/a/"+album.Slug, "error", message)
		return
	}

	if err := s.store.UpdateAlbum(r.Context(), album.ID, title,
		strings.TrimSpace(r.FormValue("description")), visibility, access); err != nil {
		s.log.Error("update album", "id", album.ID, "error", err)
		redirectNotice(w, r, "/a/"+album.Slug, "error", "Could not save the album.")
		return
	}

	s.log.Info("album updated", "album", album.ID, "actor", currentUser(r.Context()).ID,
		"visibility", string(visibility), "access", string(access))
	redirectNotice(w, r, "/a/"+album.Slug, "notice", "Album saved.")
}

// handleAlbumPage shows an album to its owner or, when shared, to anyone it is
// visible to.
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
	isOwner := canAdministerAlbum(user, album)
	canContribute := canContributeToAlbum(user, album)
	if !canViewAlbum(user, album) {
		s.notFound(w, r, "No album by that name.")
		return
	}

	// The album gates the page; each file is still listed at its own level, so
	// a private file in a shared album stays private to its owner.
	albumID := album.ID
	filter := store.FileQuery{AlbumID: &albumID}
	if user != nil {
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

	grid := s.grid(r, files, isOwner, false,
		"/a/"+album.Slug+"/files/%s/delete", "This album is empty.")
	if !isOwner && user != nil {
		// A contributor may take their own files out again, and only their own:
		// removing somebody else's stays with the owner, which is where the
		// moderation work will build on.
		owner := user.ID
		grid.RemovableOwner = &owner
	}
	if album.Access.Shared() {
		// Attribution is the point of a shared album: whose screenshot is this.
		grid.ShowOwner = true
	}

	view := albumView{
		base:          s.base(r, album.Title),
		Album:         album,
		IsOwner:       isOwner,
		CanContribute: canContribute,
		Grid:          grid,
		Levels:        models.VisibilityLevels(),
		Access:        models.AlbumAccessLevels(),
	}

	if canContribute {
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
	album, ok := s.loadDeletableAlbum(w, r)
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
//
// A contributor may only add their own files. The album decides who may add,
// never whose files may be added.
func (s *Server) handleAlbumAddFiles(w http.ResponseWriter, r *http.Request) {
	album, ok := s.loadContributableAlbum(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	user := currentUser(r.Context())
	added, closed := 0, 0

	for _, fileID := range r.Form["files"] {
		file, err := s.store.FileByID(r.Context(), fileID)
		if err != nil {
			continue // vanished or bogus id
		}
		if !ownsFile(user, file) {
			continue // never let one user file another's uploads
		}
		if err := s.store.AddFileToAlbum(r.Context(), album.ID, file.ID); err != nil {
			s.log.Error("add file to album", "album", album.ID, "file", file.ID, "error", err)
			continue
		}
		added++
		// A private file is in the album but only its owner will see it there.
		// Saying so beats letting the contributor assume everybody can.
		if file.Visibility.IsPrivate() {
			closed++
		}
	}

	notice := "Album updated."
	switch {
	case added == 1:
		notice = "1 image added."
	case added > 1:
		notice = fmt.Sprintf("%d images added.", added)
	}
	switch {
	case closed == 1:
		notice += " It is private, so only you will see it in this album."
	case closed > 1:
		notice += fmt.Sprintf(" %d of them are private, so only you will see them in this album.", closed)
	}

	if isHTMX(r) {
		hxRedirect(w, "/a/"+album.Slug)
		return
	}
	redirectNotice(w, r, "/a/"+album.Slug, "notice", notice)
}

// handleAlbumRemoveFile unlinks a file from the album.
//
// The owner may remove anything. A contributor may take back their own file,
// and only their own: removing somebody else's is the owner's call, which is
// where moderation will build on.
func (s *Server) handleAlbumRemoveFile(w http.ResponseWriter, r *http.Request) {
	album, ok := s.loadContributableAlbum(w, r)
	if !ok {
		return
	}

	user := currentUser(r.Context())
	fileID := r.PathValue("fileID")

	if !canDeleteAlbum(user, album) {
		file, err := s.store.FileByID(r.Context(), fileID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if file.UserID == nil || *file.UserID != user.ID {
			http.Error(w, "not permitted", http.StatusForbidden)
			return
		}
	}

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

// albumByRef resolves an album reference: a numeric id or a slug.
func (s *Server) albumByRef(ctx context.Context, ref string) (*models.Album, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, store.ErrNotFound
	}

	if id, convErr := strconv.ParseInt(ref, 10, 64); convErr == nil {
		return s.store.AlbumByID(ctx, id)
	}
	return s.store.AlbumBySlug(ctx, ref)
}

// ownedAlbum resolves an album the caller may administer. A missing album and
// one belonging to somebody else are reported identically, so the API cannot be
// used to probe for other people's albums.
func (s *Server) ownedAlbum(ctx context.Context, user *models.User, ref string) (*models.Album, error) {
	album, err := s.albumByRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !canAdministerAlbum(user, album) {
		return nil, store.ErrNotFound
	}
	return album, nil
}

// contributableAlbum resolves an album the caller may add their own files to,
// which on a shared album is any account that can see it. It is reported the
// same way ownedAlbum reports a miss, for the same reason.
func (s *Server) contributableAlbum(ctx context.Context, user *models.User, ref string) (*models.Album, error) {
	album, err := s.albumByRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !canContributeToAlbum(user, album) {
		return nil, store.ErrNotFound
	}
	return album, nil
}

// loadAlbum resolves {slug} to an album. A missing album and one the caller may
// not touch are reported identically by the callers, so the routes cannot be
// used to probe for other people's albums.
func (s *Server) loadAlbum(w http.ResponseWriter, r *http.Request) (*models.Album, bool) {
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
	return album, true
}

// loadAdministerableAlbum enforces ownership. Title, description, visibility
// and deletion are the owner's and the administrators'.
func (s *Server) loadAdministerableAlbum(w http.ResponseWriter, r *http.Request) (*models.Album, bool) {
	album, ok := s.loadAlbum(w, r)
	if !ok {
		return nil, false
	}
	if !canAdministerAlbum(currentUser(r.Context()), album) {
		http.Error(w, "not permitted", http.StatusForbidden)
		return nil, false
	}
	return album, true
}

// loadDeletableAlbum enforces the right to remove an album, which a moderator
// has for anybody's content.
func (s *Server) loadDeletableAlbum(w http.ResponseWriter, r *http.Request) (*models.Album, bool) {
	album, ok := s.loadAlbum(w, r)
	if !ok {
		return nil, false
	}
	if !canDeleteAlbum(currentUser(r.Context()), album) {
		http.Error(w, "not permitted", http.StatusForbidden)
		return nil, false
	}
	return album, true
}

// loadContributableAlbum enforces the right to add files, which a shared album
// grants to any account that can see it.
func (s *Server) loadContributableAlbum(w http.ResponseWriter, r *http.Request) (*models.Album, bool) {
	album, ok := s.loadAlbum(w, r)
	if !ok {
		return nil, false
	}
	if !canContributeToAlbum(currentUser(r.Context()), album) {
		http.Error(w, "not permitted", http.StatusForbidden)
		return nil, false
	}
	return album, true
}
