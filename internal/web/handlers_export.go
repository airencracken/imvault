// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"time"

	"imvault/internal/models"
	"imvault/internal/store"
)

// exportPageSize is how many file records are read at a time while building the
// manifest, so a large account does not have to fit in memory all at once.
const exportPageSize = 200

// The export is deliberately built from purpose-written types rather than from
// the model structs. Serialising models.User would put the password hash and
// the TOTP secret in a file the account can download, and that is exactly the
// kind of mistake that goes unnoticed.

type exportManifest struct {
	ExportedAt string        `json:"exported_at"`
	Account    exportAccount `json:"account"`
	Files      []exportFile  `json:"files"`
	Albums     []exportAlbum `json:"albums"`
	Note       string        `json:"note"`
}

type exportAccount struct {
	Username        string `json:"username"`
	Email           string `json:"email,omitempty"`
	EmailVerified   bool   `json:"email_verified"`
	IsAdmin         bool   `json:"is_admin"`
	CreatedAt       string `json:"created_at"`
	FileCount       int    `json:"file_count"`
	StorageUsed     int64  `json:"storage_used_bytes"`
	TwoFactorEnable bool   `json:"two_factor_enabled"`
}

type exportFile struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Path       string      `json:"path"`
	Size       int64       `json:"size_bytes"`
	Mime       string      `json:"mime"`
	Kind       string      `json:"kind"`
	Width      int         `json:"width,omitempty"`
	Height     int         `json:"height,omitempty"`
	DurationMS int64       `json:"duration_ms,omitempty"`
	FrameCount int         `json:"frame_count,omitempty"`
	SHA256     string      `json:"sha256"`
	Public     bool        `json:"public"`
	Views      int64       `json:"views"`
	CreatedAt  string      `json:"created_at"`
	ExpiresAt  *string     `json:"expires_at"`
	Tags       []exportTag `json:"tags"`
	Albums     []string    `json:"albums,omitempty"`
	URL        string      `json:"url"`
	RawURL     string      `json:"raw_url"`

	// ObjectKey is where the bytes live. It is not part of the manifest: it
	// describes this server's storage layout, which is no use to somebody
	// taking their files elsewhere.
	ObjectKey string `json:"-"`
}

type exportTag struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type exportAlbum struct {
	Title       string   `json:"title"`
	Slug        string   `json:"slug"`
	Description string   `json:"description,omitempty"`
	Public      bool     `json:"public"`
	CreatedAt   string   `json:"created_at"`
	Files       []string `json:"file_ids"`
}

// handleAccountExport streams everything an account has uploaded, plus a
// manifest describing it, as a zip.
//
// The zip is written straight to the response rather than assembled somewhere
// first: an export can be larger than the free space on the server, and there
// is no reason to write a copy of somebody's library in order to hand it to
// them. The cost is that a failure part-way through cannot be reported as an
// error status, only logged and truncated.
func (s *Server) handleAccountExport(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	ctx := r.Context()

	files, err := s.exportableFiles(r, user.ID)
	if err != nil {
		s.log.Error("export: list files", "user", user.ID, "error", err)
		http.Error(w, "could not read the account's files", http.StatusInternalServerError)
		return
	}

	albums, err := s.exportableAlbums(ctx, user.ID, files)
	if err != nil {
		s.log.Error("export: list albums", "user", user.ID, "error", err)
		http.Error(w, "could not read the account's albums", http.StatusInternalServerError)
		return
	}

	manifest := exportManifest{
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Account: exportAccount{
			Username:        user.Username,
			Email:           user.Email,
			EmailVerified:   user.EmailVerified,
			IsAdmin:         user.IsAdmin,
			CreatedAt:       user.CreatedAt.UTC().Format(time.RFC3339),
			FileCount:       len(files),
			StorageUsed:     user.StorageUsed,
			TwoFactorEnable: user.TOTPEnabled,
		},
		Files:  files,
		Albums: albums,
		Note: "Originals only. Thumbnails and previews are generated from these and " +
			"are not included. Passwords, sessions, API keys and two-factor secrets " +
			"are not exported.",
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		`attachment; filename="%s"`, exportFilename(user.Username)))
	w.Header().Set("Cache-Control", "no-store")

	archive := zip.NewWriter(w)
	defer archive.Close()

	if err := writeJSONEntry(archive, "manifest.json", manifest); err != nil {
		s.log.Error("export: write manifest", "user", user.ID, "error", err)
		return
	}

	written := 0
	for _, file := range files {
		if err := s.writeExportEntry(archive, file); err != nil {
			// The response has already begun, so the best that can be done is
			// to stop and leave the truncation visible.
			s.log.Error("export: write file", "user", user.ID, "id", file.ID, "error", err)
			return
		}
		written++
	}

	s.log.Info("account exported", "user", user.ID, "files", written)
}

// exportFilename is the name the browser saves the archive as.
func exportFilename(username string) string {
	return fmt.Sprintf("imvault-%s-%s.zip", username, time.Now().UTC().Format("2006-01-02"))
}

// exportableFiles reads the account's file metadata, paging so that a large
// library does not have to be held all at once.
func (s *Server) exportableFiles(r *http.Request, userID int64) ([]exportFile, error) {
	ctx := r.Context()
	var out []exportFile

	for offset := 0; ; offset += exportPageSize {
		batch, err := s.store.ListFiles(ctx, store.FileQuery{
			OwnerID: &userID,
			Limit:   exportPageSize,
			Offset:  offset,
		})
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}

		for _, file := range batch {
			out = append(out, exportFile{
				ID:         file.ID,
				Name:       file.OriginalName,
				Path:       exportPath(file.ID, file.OriginalName),
				Size:       file.Size,
				Mime:       file.Mime,
				Kind:       string(file.Kind),
				Width:      file.Width,
				Height:     file.Height,
				DurationMS: file.DurationMS,
				FrameCount: file.FrameCount,
				SHA256:     file.SHA256,
				Public:     file.IsPublic,
				Views:      file.Views,
				CreatedAt:  file.CreatedAt.UTC().Format(time.RFC3339),
				ExpiresAt:  exportExpiry(file),
				Tags:       exportTags(file.Tags),
				URL:        s.absoluteURL(r, "/f/"+file.ID),
				RawURL:     s.absoluteURL(r, "/f/"+file.ID+"/raw"),
				ObjectKey:  file.ObjectKey,
			})
		}

		if len(batch) < exportPageSize {
			break
		}
	}

	return out, nil
}

// exportableAlbums reads the account's albums and marks which of the exported
// files belong to each.
func (s *Server) exportableAlbums(ctx context.Context, userID int64, files []exportFile) ([]exportAlbum, error) {
	albums, err := s.store.AlbumsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	known := make(map[string]bool, len(files))
	for _, file := range files {
		known[file.ID] = true
	}

	out := make([]exportAlbum, 0, len(albums))
	for _, album := range albums {
		albumID := album.ID
		members, err := s.store.ListFiles(ctx, store.FileQuery{AlbumID: &albumID, Limit: 10000})
		if err != nil {
			return nil, err
		}

		ids := make([]string, 0, len(members))
		for _, member := range members {
			if known[member.ID] {
				ids = append(ids, member.ID)
			}
		}

		out = append(out, exportAlbum{
			Title:       album.Title,
			Slug:        album.Slug,
			Description: album.Description,
			Public:      album.IsPublic,
			CreatedAt:   album.CreatedAt.UTC().Format(time.RFC3339),
			Files:       ids,
		})
	}

	return out, nil
}

// exportPath is where an original sits inside the archive.
//
// Each file gets its own directory, so two uploads called "photo.jpg" do not
// collide and the name a person chose is preserved.
func exportPath(id, name string) string {
	return path.Join("files", id, name)
}

func exportExpiry(file *models.File) *string {
	if file.ExpiresAt == nil {
		return nil
	}
	formatted := file.ExpiresAt.UTC().Format(time.RFC3339)
	return &formatted
}

func exportTags(tags []models.Tag) []exportTag {
	out := make([]exportTag, 0, len(tags))
	for _, tag := range tags {
		out = append(out, exportTag{Name: tag.Name, Slug: tag.Slug})
	}
	return out
}

// writeJSONEntry adds a JSON document to the archive.
func writeJSONEntry(archive *zip.Writer, name string, payload any) error {
	entry, err := archive.Create(name)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(entry)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

// writeExportEntry adds one original to the archive.
//
// Stored rather than deflated: images and clips are already compressed, so
// deflating them costs processor time to save nothing.
func (s *Server) writeExportEntry(archive *zip.Writer, file exportFile) error {
	reader, err := s.objects.Open(file.ObjectKey)
	if err != nil {
		return err
	}
	defer reader.Close()

	header := &zip.FileHeader{
		Name:   file.Path,
		Method: zip.Store,
	}
	header.SetMode(0o644)

	entry, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(entry, reader)
	return err
}
