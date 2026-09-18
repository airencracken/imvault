// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"time"

	"imvault/internal/ids"
	"imvault/internal/media"
	"imvault/internal/models"
	"imvault/internal/storage"
	"imvault/internal/store"
)

const (
	// maxFilesPerUpload caps how many files one request may carry.
	maxFilesPerUpload = 20
	// multipartMemory is how much of an upload is buffered before spilling to
	// a temporary file.
	multipartMemory = 8 << 20
)

// handleUploadPage renders the uploader.
func (s *Server) handleUploadPage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	if user == nil && !s.policy().AllowAnonymousUploads {
		http.Redirect(w, r, "/login?next=%2Fupload", http.StatusSeeOther)
		return
	}

	view := uploadView{
		base:            s.base(r, "Upload"),
		MaxUploadMB:     s.fileLimit(user, media.FormatImage) >> 20,
		MaxVideoMB:      s.fileLimit(user, media.FormatWebM) >> 20,
		MaxVideoSeconds: int(s.cfg.MaxVideoDuration.Seconds()),
		VideoEnabled:    s.media.VideoEnabled(),
	}
	if user != nil {
		albums, err := s.store.AlbumsByUser(r.Context(), user.ID)
		if err != nil {
			s.log.Error("upload page: list albums", "error", err)
		}
		view.Albums = albums
	}

	s.renderPage(w, http.StatusOK, "upload", view)
}

// fileLimit is the largest single file this account may upload, for a format
// that has already been classified.
//
// An account-level override replaces the instance defaults rather than merely
// lowering them, so an administrator can raise the ceiling for somebody trusted
// as well as impose a tighter one.
func (s *Server) fileLimit(user *models.User, format media.Format) int64 {
	if user != nil && user.MaxFileBytes > 0 {
		return user.MaxFileBytes
	}
	if format.IsVideo() {
		return s.cfg.MaxVideoBytes
	}
	return s.cfg.MaxUploadBytes
}

// requestSizeLimit bounds a whole upload request, which has to allow for the
// largest file any of the callers might send.
func (s *Server) requestSizeLimit(user *models.User) int64 {
	perFile := s.cfg.MaxUploadBytes
	if s.cfg.MaxVideoBytes > perFile {
		perFile = s.cfg.MaxVideoBytes
	}
	if user != nil && user.MaxFileBytes > perFile {
		perFile = user.MaxFileBytes
	}
	return perFile*maxFilesPerUpload + (1 << 20)
}

// handleUpload accepts one or more uploads and returns the created cards.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	anonymous := user == nil
	if anonymous && !s.policy().AllowAnonymousUploads {
		http.Error(w, "anonymous uploads are disabled", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.requestSizeLimit(user))

	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		s.uploadFailure(w, r, "Could not read the upload: "+uploadErrMessage(err))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
	}()

	parts := r.MultipartForm.File["files"]
	if len(parts) == 0 {
		s.uploadFailure(w, r, "No files were included in the request.")
		return
	}
	if len(parts) > maxFilesPerUpload {
		s.uploadFailure(w, r, fmt.Sprintf("Too many files at once (limit is %d).", maxFilesPerUpload))
		return
	}

	public := r.FormValue("public") == "1"
	albumID := int64(queryInt(r, "album_id", 0))
	tags := r.FormValue("tags")

	var (
		created  []*models.File
		failures []string
	)

	for _, header := range parts {
		file, err := s.ingest(r.Context(), header, user, public)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %s", header.Filename, err))
			continue
		}
		s.applyUploadTags(r.Context(), file, tags)
		created = append(created, file)
	}

	// Attaching to an album is best-effort and only for signed-in owners.
	if albumID > 0 && user != nil && len(created) > 0 {
		s.attachUploadsToAlbum(r, user, albumID, created)
	}

	s.renderPartial(w, "upload_result", uploadResultView{
		base:      s.base(r, "Upload"),
		Grid:      s.grid(r, created, true, false, "", ""),
		Errors:    failures,
		Anonymous: anonymous,
	})
}

// applyUploadTags attaches the tags supplied with an upload.
//
// This is the only way an anonymous upload can be tagged: there is no session
// to come back and edit it with, so the names have to arrive with the bytes.
// For a signed-in uploader the same field works, and lands in their own
// namespace. A failure here never fails the upload, which has already
// succeeded by this point.
func (s *Server) applyUploadTags(ctx context.Context, file *models.File, raw string) {
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, err := s.store.AddTag(ctx, file.ID, tagOwner(file), name); err != nil {
			s.log.Error("upload: add tag", "file", file.ID, "error", err)
		}
	}
}

// attachUploadsToAlbum verifies ownership then links the new files.
func (s *Server) attachUploadsToAlbum(r *http.Request, user *models.User, albumID int64, files []*models.File) {
	album, err := s.store.AlbumByID(r.Context(), albumID)
	if err != nil || album.UserID != user.ID {
		return
	}
	for _, f := range files {
		if err := s.store.AddFileToAlbum(r.Context(), album.ID, f.ID); err != nil {
			s.log.Error("upload: attach to album", "album", albumID, "file", f.ID, "error", err)
		}
	}
}

// ingest validates, stores and records a single upload, dispatching on the
// classified media format.
func (s *Server) ingest(ctx context.Context, header *multipart.FileHeader, user *models.User, public bool) (*models.File, error) {
	src, err := header.Open()
	if err != nil {
		return nil, fmt.Errorf("could not open the upload")
	}
	defer src.Close()

	format, err := classifyUpload(src)
	if err != nil {
		return nil, err
	}

	if limit := s.fileLimit(user, format); header.Size > limit {
		return nil, fmt.Errorf("larger than the %d MiB limit for %s",
			limit>>20, kindNoun(format))
	}

	owner := userIDPtr(user)
	expires := s.expiryFor(user)
	now := time.Now().UTC()

	// Cheap pre-flight: reject an upload that cannot possibly fit before
	// decoding and writing anything.
	if owner != nil {
		if err := s.store.CheckQuota(ctx, *owner, header.Size); err != nil {
			return nil, quotaError(err, user)
		}
	}

	if format.IsVideo() {
		return s.storeVideo(ctx, src, format, header, owner, public, expires, now)
	}
	return s.storeStill(ctx, src, format, header, owner, public, expires, now)
}

// quotaError turns a store quota failure into a message worth showing a user.
func quotaError(err error, user *models.User) error {
	if !errors.Is(err, store.ErrQuotaExceeded) {
		return err
	}
	if user == nil || user.Unlimited() {
		return errors.New("storage quota exceeded")
	}
	return fmt.Errorf("storage quota exceeded (%s used of %s)",
		models.HumanSize(user.StorageUsed), models.HumanSize(user.QuotaBytes))
}

// classifyUpload sniffs the leading bytes then returns the reader to the start.
func classifyUpload(src io.ReadSeeker) (media.Format, error) {
	head := make([]byte, media.HeadSize)
	n, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("could not read the upload")
	}
	if n == 0 {
		return "", fmt.Errorf("the upload is empty")
	}

	format := media.Classify(head[:n])
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("could not rewind the upload")
	}
	return format, nil
}

// storeStill handles images, GIFs and animated WebPs.
func (s *Server) storeStill(
	ctx context.Context,
	src io.ReadSeeker,
	format media.Format,
	header *multipart.FileHeader,
	owner *int64,
	public bool,
	expires *time.Time,
	now time.Time,
) (*models.File, error) {
	sha, size, err := hashSource(src)
	if err != nil {
		return nil, err
	}

	// Identical bytes are already stored: point at them rather than decoding,
	// resizing, and writing a second copy.
	if existing, ok := s.reusableUpload(ctx, sha); ok {
		return s.recordReused(ctx, existing, header, owner, public, expires, now)
	}

	result, err := s.media.ProcessStill(src, format)
	if err != nil {
		return nil, err
	}
	s.logWarnings(result.Warnings, "")

	thumbExt, previewExt := "", ""
	if result.Thumb != nil {
		thumbExt = result.Thumb.Ext
	}
	if result.Preview != nil {
		previewExt = result.Preview.Ext
	}
	keys := s.contentKeys(sha, result.Ext, thumbExt, previewExt)

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("could not rewind the upload")
	}

	// Claim the bytes before recording the file, so two concurrent uploads
	// cannot both fit into room that only exists once.
	if owner != nil {
		if err := s.store.ReserveStorage(ctx, *owner, size); err != nil {
			return nil, quotaError(err, nil)
		}
	}

	if err := s.storeObject(keys.object, src); err != nil {
		s.releaseStorage(ctx, owner, size)
		return nil, fmt.Errorf("could not store the file")
	}

	if !s.saveRenditions(keys, result) {
		s.releaseStorage(ctx, owner, size)
		s.discardFailedUpload(ctx, sha, keys.all())
		return nil, fmt.Errorf("could not store the renditions")
	}

	// The blob has to exist before the file row: the trigger that counts
	// references fires on that insert.
	if err := s.store.EnsureBlob(ctx, sha, size, keys.object, keys.thumb, keys.preview); err != nil {
		s.releaseStorage(ctx, owner, size)
		s.discardFailedUpload(ctx, sha, keys.all())
		return nil, fmt.Errorf("could not record the content")
	}

	file := &models.File{
		ID:           ids.New(12),
		UserID:       owner,
		OriginalName: sanitiseFilename(header.Filename),
		Ext:          result.Ext,
		Mime:         result.Mime,
		Size:         size,
		Width:        result.Width,
		Height:       result.Height,
		SHA256:       sha,
		ObjectKey:    keys.object,
		ThumbKey:     keys.thumb,
		PreviewKey:   keys.preview,
		Kind:         result.Kind,
		FrameCount:   result.FrameCount,
		IsPublic:     public || owner == nil,
		CreatedAt:    now,
		ExpiresAt:    expires,
	}

	if err := s.store.CreateFile(ctx, file); err != nil {
		s.releaseStorage(ctx, owner, size)
		s.discardFailedUpload(ctx, sha, keys.all())
		return nil, fmt.Errorf("could not record the file")
	}

	return file, nil
}

// storeVideo handles WebM/MP4/MOV clips.
//
// The original is saved before probing because ffmpeg needs a real file to seek
// within, and the bytes have to be persisted regardless.
func (s *Server) storeVideo(
	ctx context.Context,
	src io.ReadSeeker,
	format media.Format,
	header *multipart.FileHeader,
	owner *int64,
	public bool,
	expires *time.Time,
	now time.Time,
) (*models.File, error) {
	sha, size, err := hashSource(src)
	if err != nil {
		return nil, err
	}

	if existing, ok := s.reusableUpload(ctx, sha); ok {
		return s.recordReused(ctx, existing, header, owner, public, expires, now)
	}

	// The original goes to its content-addressed key before probing, because
	// ffmpeg needs a real file to seek within.
	objectKey := s.contentKeys(sha, format.Ext(), "", "").object

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("could not rewind the clip")
	}

	// Claim the bytes before probing, so an over-quota upload does not pay for
	// a poster frame it will never use.
	if owner != nil {
		if err := s.store.ReserveStorage(ctx, *owner, size); err != nil {
			return nil, quotaError(err, nil)
		}
	}

	if err := s.storeObject(objectKey, src); err != nil {
		s.releaseStorage(ctx, owner, size)
		return nil, fmt.Errorf("could not store the clip")
	}

	// ffmpeg runs against the stored file when the backend is disk-backed;
	// otherwise media materialises a temporary copy.
	localPath, _ := s.objects.LocalPath(objectKey)

	result, err := s.media.ProcessVideo(ctx, src, localPath, format)
	if err != nil {
		s.releaseStorage(ctx, owner, size)
		s.discardFailedUpload(ctx, sha, []string{objectKey})
		return nil, err
	}
	s.logWarnings(result.Warnings, "")

	thumbExt := ""
	if result.Thumb != nil {
		thumbExt = result.Thumb.Ext
	}
	keys := s.contentKeys(sha, result.Ext, thumbExt, "")

	if !s.saveRenditions(keys, result) {
		s.releaseStorage(ctx, owner, size)
		s.discardFailedUpload(ctx, sha, keys.all())
		return nil, fmt.Errorf("could not store the poster frame")
	}

	if err := s.store.EnsureBlob(ctx, sha, size, keys.object, keys.thumb, ""); err != nil {
		s.releaseStorage(ctx, owner, size)
		s.discardFailedUpload(ctx, sha, keys.all())
		return nil, fmt.Errorf("could not record the content")
	}

	file := &models.File{
		ID:           ids.New(12),
		UserID:       owner,
		OriginalName: sanitiseFilename(header.Filename),
		Ext:          result.Ext,
		Mime:         result.Mime,
		Size:         size,
		Width:        result.Width,
		Height:       result.Height,
		SHA256:       sha,
		ObjectKey:    keys.object,
		ThumbKey:     keys.thumb,
		PreviewKey:   keys.preview,
		Kind:         result.Kind,
		DurationMS:   result.DurationMS,
		IsPublic:     public || owner == nil,
		CreatedAt:    now,
		ExpiresAt:    expires,
	}

	if err := s.store.CreateFile(ctx, file); err != nil {
		s.releaseStorage(ctx, owner, size)
		s.discardFailedUpload(ctx, sha, keys.all())
		return nil, fmt.Errorf("could not record the clip")
	}
	return file, nil
}

// objectKeys bundles the storage keys for one stored upload.
type objectKeys struct {
	object  string
	thumb   string
	preview string
}

func (k objectKeys) all() []string {
	out := make([]string, 0, 3)
	for _, key := range []string{k.object, k.thumb, k.preview} {
		if key != "" {
			out = append(out, key)
		}
	}
	return out
}

// saveRenditions writes the thumbnail and preview. On failure it removes
// anything it already wrote and reports false.
func (s *Server) saveRenditions(keys objectKeys, result *media.Result) bool {
	if result.Thumb != nil && keys.thumb != "" {
		if _, err := s.objects.Save(keys.thumb, bytes.NewReader(result.Thumb.Data)); err != nil {
			s.deleteKeys(keys.all())
			return false
		}
	}
	if result.Preview != nil && keys.preview != "" {
		if _, err := s.objects.Save(keys.preview, bytes.NewReader(result.Preview.Data)); err != nil {
			s.deleteKeys(keys.all())
			return false
		}
	}
	return true
}

func (s *Server) deleteKeys(keys []string) {
	for _, key := range keys {
		if err := s.objects.Delete(key); err != nil && !errors.Is(err, storage.ErrNotFound) {
			s.log.Error("delete object", "key", key, "error", err)
		}
	}
}

// logWarnings surfaces non-fatal media problems.
func (s *Server) logWarnings(warnings []string, id string) {
	for _, w := range warnings {
		s.log.Warn("media", "id", id, "warning", w)
	}
}

// releaseStorage gives reserved bytes back after a failed upload.
func (s *Server) releaseStorage(ctx context.Context, owner *int64, size int64) {
	if owner == nil || size <= 0 {
		return
	}
	if err := s.store.ReleaseStorage(ctx, *owner, size); err != nil {
		s.log.Error("release storage", "user", *owner, "size", size, "error", err)
	}
}

// userIDPtr converts a user into the nullable owner column value.
//
// A nil user is an anonymous uploader, and nil also serves as the anonymous
// viewer scope for visibility filtering.
func userIDPtr(user *models.User) *int64 {
	if user == nil {
		return nil
	}
	id := user.ID
	return &id
}

// expiryFor gives anonymous uploads a retention deadline.
func (s *Server) expiryFor(user *models.User) *time.Time {
	if user != nil {
		return nil
	}
	// Read at upload time, so an administrator changing the window affects new
	// uploads immediately and existing ones through ApplyAnonymousRetention.
	e := time.Now().UTC().Add(s.policy().AnonymousTTL)
	return &e
}

// uploadFailure reports a whole-request upload error.
func (s *Server) uploadFailure(w http.ResponseWriter, r *http.Request, message string) {
	if !isHTMX(r) {
		http.Error(w, message, http.StatusBadRequest)
		return
	}
	s.renderPartial(w, "upload_result", uploadResultView{
		base:   s.base(r, "Upload"),
		Grid:   s.grid(r, nil, false, false, "", ""),
		Errors: []string{message},
	})
}

func uploadErrMessage(err error) string {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return fmt.Sprintf("the request exceeded the %d MiB limit", maxErr.Limit>>20)
	}
	if strings.Contains(err.Error(), "multipart") {
		return "the form data was malformed"
	}
	return "the upload could not be processed"
}

// kindNoun describes a format for error messages.
func kindNoun(format media.Format) string {
	if format.IsVideo() {
		return "clips"
	}
	return "images"
}

// sanitiseFilename strips any path components a client may have sent.
func sanitiseFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		return "untitled"
	}
	if len(name) > 200 {
		name = models.Truncate(name, 200)
	}
	return name
}

// deleteFileObjects releases a file's content, unless another upload shares it.
//
// Called after the row is gone: the trigger on that delete has already brought
// the reference count down, so a count of zero here means the bytes are free.
func (s *Server) deleteFileObjects(ctx context.Context, f *models.File) {
	s.releaseBlob(ctx, f.SHA256)
}

// deleteFileAndRelease removes a file's row and bytes, giving its owner their
// storage back.
func (s *Server) deleteFileAndRelease(ctx context.Context, file *models.File) error {
	if err := s.store.DeleteFile(ctx, file.ID); err != nil {
		return err
	}
	s.deleteFileObjects(ctx, file)
	s.releaseStorage(ctx, file.UserID, file.Size)
	return nil
}
