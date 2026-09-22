// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"imvault/internal/metadata"
	"imvault/internal/models"
	"imvault/internal/store"
)

// apiFileJSON is the wire representation of a stored file.
type apiFileJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Mime        string `json:"mime"`
	Kind        string `json:"kind"`
	Size        int64  `json:"size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Views       int64  `json:"views"`
	// Public is the older boolean, kept so clients written against two levels
	// keep working. It is true only at the public level.
	Public bool `json:"public"`
	// Visibility is the level itself: public, members, or private.
	Visibility string `json:"visibility"`
	// Metadata is what happens to the camera details, date, and location.
	Metadata string `json:"metadata"`
	// Details is what the file says about itself. The API returns an account's
	// own files to that account, so nothing is withheld here.
	Details    *metadata.Details `json:"details,omitempty"`
	DurationMS int64             `json:"duration_ms,omitempty"`
	FrameCount int               `json:"frame_count,omitempty"`
	CreatedAt  string            `json:"created_at"`
	ExpiresAt  *string           `json:"expires_at"`
	Tags       []apiTagJSON      `json:"tags"`

	PageURL  string `json:"page_url"`
	RawURL   string `json:"raw_url"`
	ThumbURL string `json:"thumb_url"`
	// URL is the direct link, which is what most clients want to paste back.
	URL string `json:"url"`
}

// apiTagJSON is the wire representation of a tag.
type apiTagJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Username string `json:"username"`
	Count    int    `json:"count,omitempty"`
}

// apiUploadResponse is returned by POST /api/v1/upload.
type apiUploadResponse struct {
	Files  []apiFileJSON `json:"files"`
	Errors []string      `json:"errors,omitempty"`
}

func newAPIFile(r *http.Request, s *Server, f *models.File) apiFileJSON {
	out := apiFileJSON{
		ID:          f.ID,
		Name:        f.OriginalName,
		Description: f.Description,
		Mime:        f.Mime,
		Kind:        string(f.Kind),
		Size:        f.Size,
		Width:       f.Width,
		Height:      f.Height,
		Views:       f.Views,
		Public:      f.Visibility.IsPublic(),
		Visibility:  string(f.Visibility),
		Metadata:    string(f.Metadata),
		Details:     metadata.DecodeDetails(f.Details),
		DurationMS:  f.DurationMS,
		FrameCount:  f.FrameCount,
		CreatedAt:   f.CreatedAt.UTC().Format(time.RFC3339),
		Tags:        newAPITags(f.Tags),
		PageURL:     s.absoluteURL(r, "/f/"+f.ID),
		RawURL:      s.absoluteURL(r, "/f/"+f.ID+"/raw"),
		ThumbURL:    s.absoluteURL(r, f.ThumbURL()),
	}
	out.URL = out.RawURL
	if f.ExpiresAt != nil {
		formatted := f.ExpiresAt.UTC().Format(time.RFC3339)
		out.ExpiresAt = &formatted
	}
	return out
}

// newAPITags converts tags, always returning a non-nil slice so the JSON shows
// an empty array rather than null.
func newAPITags(tags []models.Tag) []apiTagJSON {
	out := make([]apiTagJSON, 0, len(tags))
	for _, t := range tags {
		out = append(out, apiTagJSON{
			ID:       t.ID,
			Name:     t.Name,
			Slug:     t.Slug,
			Username: t.Username,
			Count:    t.Count,
		})
	}
	return out
}

// apiOwnedFile loads {id} and enforces ownership, writing the error response
// itself. Files the caller does not own are reported as missing.
func (s *Server) apiOwnedFile(w http.ResponseWriter, r *http.Request, user *models.User) (*models.File, bool) {
	file, err := s.store.FileByID(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "no such file")
			return nil, false
		}
		s.log.Error("api: load file", "id", r.PathValue("id"), "error", err)
		writeAPIError(w, http.StatusInternalServerError, "database error")
		return nil, false
	}
	if !canChangeFile(user, file) {
		writeAPIError(w, http.StatusNotFound, "no such file")
		return nil, false
	}
	return file, true
}

// writeJSON emits a JSON response.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

// writeAPIError emits a JSON error envelope.
func writeAPIError(w http.ResponseWriter, status int, message string) {
	noStore(w)
	writeJSON(w, status, map[string]string{"error": message})
}

// apiMe identifies the caller, which is handy for verifying a key works.
func (s *Server) apiMe(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	if user == nil {
		writeAPIError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       user.ID,
		"username": user.Username,
		"email":    user.Email,
		"role":     string(user.Role),
		// admin and can_moderate are kept for clients written against the
		// administrator flag; role is the field to read now.
		"admin":        user.IsAdmin(),
		"can_moderate": user.CanModerate(),
		"created":      user.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// apiUpload accepts one or more files, exactly like the web uploader, and
// replies with JSON (or plain-text URLs when ?format=text).
func (s *Server) apiUpload(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	if user == nil {
		writeAPIError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// requestLimitsMW holds the shared upload slot and bounds the body.
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		writeAPIError(w, http.StatusBadRequest, "could not read the upload: "+uploadErrMessage(err))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
	}()

	parts := r.MultipartForm.File["files"]
	if len(parts) == 0 {
		// Accept the field name several clients default to.
		for _, name := range []string{"file", "image", "uploads"} {
			if candidates := r.MultipartForm.File[name]; len(candidates) > 0 {
				parts = candidates
				break
			}
		}
	}
	if len(parts) == 0 {
		writeAPIError(w, http.StatusBadRequest, "no files were included in the request")
		return
	}
	if len(parts) > maxFilesPerUpload {
		writeAPIError(w, http.StatusBadRequest,
			fmt.Sprintf("too many files at once (limit is %d)", maxFilesPerUpload))
		return
	}

	// The level comes from the same form the web uploader uses, so a script and
	// a person get the same policy: an explicit choice, then the older public
	// boolean, then the instance default. The default matters more than it
	// looks: on an instance configured for members, a script that says nothing
	// uploads for members rather than for the world.
	options := s.uploadOptions(r, user)

	var (
		created  []apiFileJSON
		failures []string
	)

	for _, header := range parts {
		file, err := s.ingest(r.Context(), header, user, options)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %s", header.Filename, err))
			continue
		}

		s.applyUploadTags(r.Context(), file, r.FormValue("tags"))

		if albumRef := strings.TrimSpace(r.FormValue("album")); albumRef != "" {
			s.apiAttachToAlbum(r, user, albumRef, file)
		}
		created = append(created, newAPIFile(r, s, file))
	}

	if len(created) == 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, strings.Join(failures, "; "))
		return
	}

	noStore(w)
	if wantsPlainText(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		for _, f := range created {
			io.WriteString(w, f.URL+"\n")
		}
		return
	}

	writeJSON(w, http.StatusCreated, apiUploadResponse{Files: created, Errors: failures})
}

// apiAttachToAlbum links an upload to an album named by id or slug, when the
// caller owns it. Failures are logged rather than failing the upload, which has
// already succeeded by this point.
func (s *Server) apiAttachToAlbum(r *http.Request, user *models.User, ref string, file *models.File) {
	album, err := s.ownedAlbum(r.Context(), user, ref)
	if err != nil {
		s.log.Warn("api upload: album not usable", "album", ref, "user", user.ID)
		return
	}

	if err := s.store.AddFileToAlbum(r.Context(), album.ID, file.ID); err != nil {
		s.log.Error("api upload: attach to album", "album", album.ID, "file", file.ID, "error", err)
	}
}

// apiFile returns metadata for one file the caller owns.
func (s *Server) apiFile(w http.ResponseWriter, r *http.Request) {
	file, ok := s.apiOwnedFile(w, r, currentUser(r.Context()))
	if !ok {
		return
	}

	noStore(w)
	writeJSON(w, http.StatusOK, newAPIFile(r, s, file))
}

// apiPatchFile updates supplied fields together, after validating the payload.
func (s *Server) apiPatchFile(w http.ResponseWriter, r *http.Request) {
	file, ok := s.apiOwnedFile(w, r, currentUser(r.Context()))
	if !ok {
		return
	}

	params, err := newParams(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	update, err := parseFileUpdate(params)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.UpdateFile(r.Context(), file.ID, update); err != nil {
		s.log.Error("api: update file", "id", file.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "could not update the file")
		return
	}
	s.apiFile(w, r)
}

func parseFileUpdate(params *params) (store.FileUpdate, error) {
	var update store.FileUpdate
	visibility, hasVisibility, err := params.visibility()
	if err != nil {
		return update, err
	}
	if hasVisibility {
		update.Visibility = &visibility
	}
	if raw := params.str("metadata"); raw != "" {
		metadata := models.MetadataPolicy(strings.ToLower(strings.TrimSpace(raw)))
		if !metadata.Valid() {
			return update, fmt.Errorf("%q is not a metadata setting (use shown, inherit, or hidden)", raw)
		}
		update.Metadata = &metadata
	}
	update.Description, err = params.description()
	if err != nil {
		return update, err
	}
	if update.Visibility == nil && update.Metadata == nil && update.Description == nil {
		return update, errors.New("no supported fields were provided (supported: visibility, public, metadata, description)")
	}
	return update, nil
}

// apiListFiles returns the caller's own uploads, newest first, with optional
// filtering by search term, tag, album, visibility and kind.
func (s *Server) apiListFiles(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	query := r.URL.Query()

	limit := queryInt(r, "limit", 50)
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := queryInt(r, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	filter := store.FileQuery{
		OwnerID: &user.ID,
		Search:  query.Get("q"),
		Limit:   limit,
		Offset:  offset,
	}

	if ref := strings.TrimSpace(query.Get("tag")); ref != "" {
		// This endpoint only ever lists the caller's own uploads, so it resolves
		// within their namespace.
		tag, err := s.store.TagByRefInNamespace(r.Context(), &user.ID, ref)
		if err != nil {
			writeAPIError(w, http.StatusNotFound, "no such tag")
			return
		}
		tagID := tag.ID
		filter.TagID = &tagID
	}

	if ref := strings.TrimSpace(query.Get("album")); ref != "" {
		album, err := s.ownedAlbum(r.Context(), user, ref)
		if err != nil {
			writeAPIError(w, http.StatusNotFound, "no such album")
			return
		}
		albumID := album.ID
		filter.AlbumID = &albumID
	}

	if err := applyMediaFilters(query, &filter); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	total, err := s.store.CountFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("api: count files", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "database error")
		return
	}

	files, err := s.store.ListFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("api: list files", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "database error")
		return
	}

	out := make([]apiFileJSON, 0, len(files))
	for _, f := range files {
		out = append(out, newAPIFile(r, s, f))
	}

	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"files":  out,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// applyMediaFilters validates the scalar filters independently of resolving
// account-owned tags and albums. An explicit visibility wins over public.
func applyMediaFilters(query url.Values, filter *store.FileQuery) error {
	if raw := strings.TrimSpace(query.Get("visibility")); raw != "" {
		level, err := parseVisibilityStrict(raw)
		if err != nil {
			return err
		}
		filter.Visibility = &level
	} else if raw := strings.TrimSpace(query.Get("public")); raw != "" {
		filter.PublicOnly = apiBool(raw)
		filter.NotPublic = !filter.PublicOnly
	}
	if raw := strings.TrimSpace(query.Get("kind")); raw != "" {
		kind, err := parseKindFilter(raw)
		if err != nil {
			return err
		}
		filter.Kind = &kind
	}
	return nil
}

// parseKindFilter accepts the friendly names scripts tend to use.
func parseKindFilter(raw string) (models.Kind, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "image", "images", "still", "stills":
		return models.KindImage, nil
	case "animated", "animation", "animations", "gif", "gifs":
		return models.KindAnimated, nil
	case "video", "videos", "clip", "clips":
		return models.KindVideo, nil
	default:
		return "", fmt.Errorf("unknown kind %q (expected image, animated or video)", raw)
	}
}

// apiDeleteFile removes a file the caller owns, bytes included.
func (s *Server) apiDeleteFile(w http.ResponseWriter, r *http.Request) {
	file, ok := s.apiOwnedFile(w, r, currentUser(r.Context()))
	if !ok {
		return
	}

	if err := s.deleteFileAndRelease(r.Context(), file); err != nil {
		s.log.Error("api: delete file", "id", file.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "could not delete the file")
		return
	}

	noStore(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": file.ID})
}

// wantsPlainText reports whether the client asked for bare URLs, which is what
// ShareX-style custom uploaders expect.
func wantsPlainText(r *http.Request) bool {
	switch strings.ToLower(r.URL.Query().Get("format")) {
	case "text", "plain", "url":
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "text/plain")
}

// maxAPIBodyBytes bounds a JSON request body on the write endpoints.
const maxAPIBodyBytes = 1 << 20

// params is a request payload that may arrive either as a JSON object or as
// ordinary form encoding, so the same endpoint serves curl, scripts and
// browsers without ceremony.
type params struct {
	form url.Values
	json map[string]json.RawMessage
}

// newParams decodes the request body according to its content type. An empty
// body is valid and yields empty params.
func newParams(r *http.Request) (*params, error) {
	p := &params{}

	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxAPIBodyBytes+1))
		if err != nil {
			return nil, fmt.Errorf("could not read the request body")
		}
		if len(body) > maxAPIBodyBytes {
			return nil, fmt.Errorf("the request body is too large")
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return p, nil
		}
		if err := json.Unmarshal(body, &p.json); err != nil {
			return nil, fmt.Errorf("the request body is not a valid JSON object")
		}
		return p, nil
	}

	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("could not parse the form body")
	}
	p.form = r.Form
	return p, nil
}

// str returns a trimmed string field.
func (p *params) str(key string) string {
	if p.json != nil {
		raw, ok := p.json[key]
		if !ok {
			return ""
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return strings.TrimSpace(s)
		}
		// Accept numbers where a string is expected, so ids work either way.
		var n json.Number
		if err := json.Unmarshal(raw, &n); err == nil {
			return n.String()
		}
		return ""
	}
	return strings.TrimSpace(p.form.Get(key))
}

// visibility reads a visibility level, accepting the older public boolean as a
// synonym. The third return value is the error, which is separate from absence
// so a caller can reject a level it does not recognise rather than silently
// closing the file.
func (p *params) visibility() (models.Visibility, bool, error) {
	if raw := p.str("visibility"); raw != "" {
		level, err := parseVisibilityStrict(raw)
		return level, true, err
	}
	if public, present := p.boolPtr("public"); present {
		if public {
			return models.VisibilityPublic, true, nil
		}
		return models.VisibilityPrivate, true, nil
	}
	return "", false, nil
}

// parseVisibilityStrict is ParseVisibility for input a person typed, where a
// typo should be reported rather than quietly treated as private.
func parseVisibilityStrict(raw string) (models.Visibility, error) {
	level := models.Visibility(strings.ToLower(strings.TrimSpace(raw)))
	if !level.Valid() {
		return "", fmt.Errorf("%q is not a visibility level (use public, members, or private)", raw)
	}
	return level, nil
}

// boolPtr returns a boolean field and whether it was present at all, which
// matters for partial updates.
func (p *params) boolPtr(key string) (bool, bool) {
	if p.json != nil {
		raw, ok := p.json[key]
		if !ok {
			return false, false
		}
		var b bool
		if err := json.Unmarshal(raw, &b); err == nil {
			return b, true
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return apiBool(s), true
		}
		return false, false
	}
	if _, ok := p.form[key]; !ok {
		return false, false
	}
	return apiBool(p.form.Get(key)), true
}

// strs returns a repeated field, accepting either a JSON array or a single
// JSON string.
func (p *params) strs(key string) []string {
	if p.json != nil {
		raw, ok := p.json[key]
		if !ok {
			return nil
		}
		var list []string
		if err := json.Unmarshal(raw, &list); err == nil {
			return list
		}
		var single string
		if err := json.Unmarshal(raw, &single); err == nil {
			return []string{single}
		}
		return nil
	}
	return p.form[key]
}

// apiBool parses the loose boolean values scripts tend to send.
func apiBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
