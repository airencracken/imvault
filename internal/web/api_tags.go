// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strconv"
	"strings"

	"imvault/internal/models"
)

// apiTagsResponse is returned by the tag endpoints.
type apiTagsResponse struct {
	Tags []apiTagJSON `json:"tags"`
}

// apiListTags returns the tags that label files the caller can see, most used
// first. Counts are computed over visible files only, so tags that exist solely
// on other accounts' private uploads are not exposed.
func (s *Server) apiListTags(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 200)
	if limit < 1 || limit > 500 {
		limit = 200
	}

	tags, err := s.store.ListTags(r.Context(), userIDPtr(currentUser(r.Context())), limit)
	if err != nil {
		s.log.Error("api: list tags", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "database error")
		return
	}

	noStore(w)
	writeJSON(w, http.StatusOK, apiTagsResponse{Tags: newAPITags(tags)})
}

// apiAddFileTag attaches a tag to a file the caller owns, creating the tag if
// it does not exist yet.
func (s *Server) apiAddFileTag(w http.ResponseWriter, r *http.Request) {
	file, ok := s.apiOwnedFile(w, r, currentUser(r.Context()))
	if !ok {
		return
	}

	params, err := newParams(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := params.str("name")
	if name == "" {
		name = params.str("tag")
	}
	if name == "" {
		writeAPIError(w, http.StatusBadRequest, "a tag name is required")
		return
	}

	// Tags live in the file owner's namespace, not the caller's.
	ownerID, err := tagOwner(file)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	if _, err := s.store.AddTag(r.Context(), file.ID, ownerID, name); err != nil {
		s.log.Error("api: add tag", "file", file.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "could not add the tag")
		return
	}

	s.writeFileTags(w, r, file.ID)
}

// apiRemoveFileTag detaches a tag from a file the caller owns, accepting a tag
// id, slug or name in the path.
//
// The tag is matched against the file's own tags rather than resolved globally,
// so the endpoint cannot confirm the existence of tag names that only appear on
// other people's uploads.
func (s *Server) apiRemoveFileTag(w http.ResponseWriter, r *http.Request) {
	file, ok := s.apiOwnedFile(w, r, currentUser(r.Context()))
	if !ok {
		return
	}

	ref := strings.TrimSpace(r.PathValue("tagRef"))

	tags, err := s.store.TagsForFile(r.Context(), file.ID)
	if err != nil {
		s.log.Error("api: load file tags", "file", file.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "database error")
		return
	}

	tag := matchTag(tags, ref)
	if tag == nil {
		writeAPIError(w, http.StatusNotFound, "no such tag on this file")
		return
	}

	if err := s.store.RemoveTag(r.Context(), file.ID, tag.ID); err != nil {
		s.log.Error("api: remove tag", "file", file.ID, "tag", tag.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "could not remove the tag")
		return
	}

	s.writeFileTags(w, r, file.ID)
}

// matchTag finds a tag by numeric id, slug or name within a known set.
func matchTag(tags []models.Tag, ref string) *models.Tag {
	if ref == "" {
		return nil
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for i := range tags {
			if tags[i].ID == id {
				return &tags[i]
			}
		}
		return nil
	}
	for i := range tags {
		if tags[i].Slug == ref || strings.EqualFold(tags[i].Name, ref) {
			return &tags[i]
		}
	}
	return nil
}

// writeFileTags responds with the file's tag list after a change, so a client
// always sees the resulting state rather than having to re-fetch.
func (s *Server) writeFileTags(w http.ResponseWriter, r *http.Request, fileID string) {
	tags, err := s.store.TagsForFile(r.Context(), fileID)
	if err != nil {
		s.log.Error("api: load file tags", "file", fileID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "database error")
		return
	}

	noStore(w)
	writeJSON(w, http.StatusOK, apiTagsResponse{Tags: newAPITags(tags)})
}
