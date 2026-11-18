// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"

	"imvault/internal/models"
	"imvault/internal/store"
)

type uploadWarning struct {
	FileID  string `json:"file_id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

type skippedUpload struct {
	FileID string `json:"file_id"`
	Name   string `json:"name"`
}

type duplicateUpload struct{ id string }

func (*duplicateUpload) Error() string { return "already in your library" }

type uploadBatchResult struct {
	Files    []*models.File
	Errors   []string
	Warnings []uploadWarning
	Skipped  []skippedUpload
}

func (s *Server) uploadBatch(r *http.Request, parts []*multipart.FileHeader, user *models.User, album string) uploadBatchResult {
	var result uploadBatchResult
	options := s.uploadOptions(r, user)
	tags := strings.Split(r.FormValue("tags"), ",")
	for _, header := range parts {
		file, err := s.ingest(r.Context(), header, user, options)
		var duplicate *duplicateUpload
		switch {
		case errors.As(err, &duplicate):
			result.Skipped = append(result.Skipped, skippedUpload{FileID: duplicate.id, Name: header.Filename})
		case err != nil:
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", header.Filename, err))
		default:
			result.Files = append(result.Files, file)
			for _, message := range s.organizeUpload(r.Context(), user, file, tags, album) {
				result.Warnings = append(result.Warnings, uploadWarning{FileID: file.ID, Name: file.OriginalName, Message: message})
			}
		}
	}
	return result
}

// organizeUpload reports failures separately from the saved file. A retry can
// organize that file through the existing gallery actions.
func (s *Server) organizeUpload(ctx context.Context, user *models.User, file *models.File, tags []string, albumRef string) []string {
	var warnings []string
	for _, raw := range tags {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		tag, err := s.store.AddTag(ctx, file.ID, tagOwner(file), name)
		if err != nil {
			s.log.Error("upload: add tag", "file", file.ID, "error", err)
			warnings = append(warnings, fmt.Sprintf("Tag %q could not be added.", name))
			continue
		}
		if !hasUploadTag(file.Tags, tag.ID) {
			file.Tags = append(file.Tags, *tag)
		}
	}
	if albumRef != "" && user != nil {
		if err := s.addUploadToAlbum(ctx, user, albumRef, file.ID); err != nil {
			s.log.Warn("upload: attach to album", "file", file.ID, "error", err)
			warnings = append(warnings, "The selected album could not be added.")
		}
	}
	return warnings
}

func hasUploadTag(tags []models.Tag, id int64) bool {
	for _, tag := range tags {
		if tag.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) addUploadToAlbum(ctx context.Context, user *models.User, ref, fileID string) error {
	album, err := s.contributableAlbum(ctx, user, ref)
	if err != nil {
		return err
	}
	_, err = s.store.AddFilesToAlbum(ctx, user.ID, []string{fileID}, album.ID)
	return err
}

func (s *Server) checkUploadDuplicate(ctx context.Context, owner int64, sha string) error {
	id, err := s.store.ActiveUploadByHash(ctx, owner, sha)
	switch {
	case err == nil:
		return &duplicateUpload{id: id}
	case errors.Is(err, store.ErrNotFound):
		return nil
	default:
		return errors.New("could not check for an existing upload")
	}
}

func (s *Server) validUploadForm(w http.ResponseWriter, r *http.Request) bool {
	values := r.PostForm["skip_duplicates"]
	if len(values) > 1 || (len(values) == 1 && values[0] != "0" && values[0] != "1") {
		const message = "Choose whether to skip identical files."
		if isAPIPath(r) {
			writeAPIError(w, http.StatusBadRequest, message)
		} else {
			s.uploadFailure(w, r, http.StatusBadRequest, message)
		}
		return false
	}
	return s.validSharingForm(w, r)
}
