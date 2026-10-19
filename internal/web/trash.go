// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"errors"
	"net/http"
	"time"

	"imvault/internal/models"
	"imvault/internal/store"
)

type trashView struct {
	base
	Files      []*models.File
	Pagination paginationView
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	owner := currentUser(r.Context()).ID
	total, err := s.store.CountTrash(r.Context(), owner)
	if err != nil {
		http.Error(w, "could not load Trash", 500)
		return
	}
	page, pg := pagination(r, total)
	files, err := s.store.ListTrash(r.Context(), owner, defaultPageSize, (page-1)*defaultPageSize)
	if err != nil {
		http.Error(w, "could not load Trash", 500)
		return
	}
	noStore(w)
	s.renderPage(w, 200, "trash", trashView{base: s.base(r, "Trash"), Files: files, Pagination: pg})
}

func (s *Server) handleTrashThumb(w http.ResponseWriter, r *http.Request) {
	file, err := s.store.TrashedFile(r.Context(), r.PathValue("id"), currentUser(r.Context()).ID)
	if err != nil {
		s.trashError(w, r, err)
		return
	}
	if !file.TrashedAt.Add(store.TrashRetention).After(time.Now()) {
		http.NotFound(w, r)
		return
	}
	file.Visibility = models.VisibilityPrivate
	if file.Rotation != 0 {
		s.serveRotatedPhoto(w, r, file, file.ThumbKey, false)
		return
	}
	s.serveObject(w, r, file, file.ThumbKey, mimeForKey(file.ThumbKey), "inline")
}

func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	err := s.store.RestoreTrashedFile(r.Context(), r.PathValue("id"), currentUser(r.Context()).ID, time.Now())
	if err != nil {
		s.trashError(w, r, err)
		return
	}
	s.redirectFlash(w, r, "/trash", flashNotice, "Restored with its previous sharing settings, tags and album membership.")
}

func (s *Server) handleTrashPurge(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("confirm") != "yes" {
		http.Error(w, "confirm permanent deletion", http.StatusBadRequest)
		return
	}
	file, err := s.store.PurgeTrashedFile(r.Context(), r.PathValue("id"), currentUser(r.Context()).ID)
	if err != nil {
		s.trashError(w, r, err)
		return
	}
	s.deleteFileObjects(r.Context(), file)
	s.releaseStorage(r.Context(), file.UserID, file.Size)
	s.redirectFlash(w, r, "/trash", flashNotice, "Permanently deleted.")
}

func (s *Server) trashError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	s.log.Error("trash", "error", err)
	http.Error(w, "could not update Trash", http.StatusInternalServerError)
}

func (s *Server) apiRestoreTrash(w http.ResponseWriter, r *http.Request) {
	err := s.store.RestoreTrashedFile(r.Context(), r.PathValue("id"), currentUser(r.Context()).ID, time.Now())
	if err != nil {
		s.apiTrashError(w, err)
		return
	}
	noStore(w)
	writeJSON(w, 200, map[string]string{"status": "restored", "id": r.PathValue("id")})
}

func (s *Server) apiPurgeTrash(w http.ResponseWriter, r *http.Request) {
	file, err := s.store.PurgeTrashedFile(r.Context(), r.PathValue("id"), currentUser(r.Context()).ID)
	if err != nil {
		s.apiTrashError(w, err)
		return
	}
	s.deleteFileObjects(r.Context(), file)
	s.releaseStorage(r.Context(), file.UserID, file.Size)
	noStore(w)
	writeJSON(w, 200, map[string]string{"status": "deleted", "id": file.ID})
}

func (s *Server) apiTrashError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, 404, "no such trashed file, or its restore window has ended")
		return
	}
	s.log.Error("api trash", "error", err)
	writeAPIError(w, 500, "could not update Trash")
}

type trashFileJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	TrashedAt  string `json:"trashed_at"`
	PurgeAfter string `json:"purge_after"`
}

func (s *Server) apiListTrash(w http.ResponseWriter, r *http.Request) {
	owner := currentUser(r.Context()).ID
	limit, offset := queryInt(r, "limit", 50), queryInt(r, "offset", 0)
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	total, err := s.store.CountTrash(r.Context(), owner)
	if err != nil {
		s.apiTrashError(w, err)
		return
	}
	files, err := s.store.ListTrash(r.Context(), owner, limit, offset)
	if err != nil {
		s.apiTrashError(w, err)
		return
	}
	out := make([]trashFileJSON, 0, len(files))
	for _, f := range files {
		out = append(out, trashFileJSON{ID: f.ID, Name: f.OriginalName, Size: f.Size, TrashedAt: f.TrashedAt.UTC().Format(time.RFC3339), PurgeAfter: f.TrashedAt.Add(store.TrashRetention).UTC().Format(time.RFC3339)})
	}
	noStore(w)
	writeJSON(w, 200, map[string]any{"files": out, "total": total, "limit": limit, "offset": offset})
}
