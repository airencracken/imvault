// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"imvault/internal/apikeys"
	"imvault/internal/models"
	"imvault/internal/store"
)

// seedUser creates an account directly, bypassing the registration form.
func (h *harness) seedUser(username string) *models.User {
	h.t.Helper()

	user, err := h.store.CreateUser(h.t.Context(), store.NewUser{Username: username, Email: username + "@example.com", PasswordHash: "hash"})
	if err != nil {
		h.t.Fatalf("seed user: %v", err)
	}
	return user
}

// seedKey mints an API key for a user and returns the full secret.
func (h *harness) seedKey(userID int64, name string, expiresAt *time.Time) string {
	h.t.Helper()

	gen := apikeys.Generate()
	if _, err := h.store.CreateAPIKey(h.t.Context(), userID, name, gen.Prefix, gen.Hash, expiresAt); err != nil {
		h.t.Fatalf("seed api key: %v", err)
	}
	return gen.Full
}

// apiDo performs a request with no cookie jar at all, which proves that the
// bearer token alone authenticates it.
func (h *harness) apiDo(method, path, key string, body io.Reader, contentType string) (*http.Response, []byte) {
	h.t.Helper()

	req, err := http.NewRequest(method, h.server.URL+path, body)
	if err != nil {
		h.t.Fatalf("build %s %s: %v", method, path, err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read %s %s: %v", method, path, err)
	}
	return resp, out
}

// apiUpload builds a multipart upload body without a CSRF token.
func (h *harness) apiUpload(key string, fields map[string]string, files map[string][]byte) (*http.Response, []byte) {
	h.t.Helper()

	var body strings.Builder
	writer := multipart.NewWriter(&body)

	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			h.t.Fatal(err)
		}
	}
	for filename, data := range files {
		part, err := writer.CreateFormFile("files", filename)
		if err != nil {
			h.t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		h.t.Fatal(err)
	}

	return h.apiDo(http.MethodPost, "/api/v1/upload", key,
		strings.NewReader(body.String()), writer.FormDataContentType())
}

func decodeUpload(t *testing.T, raw []byte) apiUploadResponse {
	t.Helper()

	var parsed apiUploadResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("decode upload response: %v (body: %s)", err, truncate(string(raw)))
	}
	return parsed
}

var revealedKeyRe = regexp.MustCompile(`value="(imv_[A-Za-z0-9]+_[A-Za-z0-9]+)"`)

// extractKey pulls the one-time revealed key out of the panel fragment.
func extractKey(html string) string {
	match := revealedKeyRe.FindStringSubmatch(html)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func TestAPIRequiresAKey(t *testing.T) {
	h := newHarness(t)

	resp, body := h.apiDo(http.MethodPost, "/api/v1/upload", "", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("401 response is missing a WWW-Authenticate header")
	}
	if !strings.Contains(string(body), "API key") {
		t.Errorf("body = %s, want a JSON error mentioning the key", truncate(string(body)))
	}

	// A well-formed but unknown key must be rejected.
	unknown := apikeys.Generate()
	if resp, _ := h.apiDo(http.MethodGet, "/api/v1/me", unknown.Full, nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unknown key status = %d, want 401", resp.StatusCode)
	}

	// So must a key whose secret does not match its prefix.
	user := h.seedUser("alice")
	real := h.seedKey(user.ID, "laptop", nil)
	tampered := real[:len(real)-1] + "z"
	if resp, _ := h.apiDo(http.MethodGet, "/api/v1/me", tampered, nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("tampered key status = %d, want 401", resp.StatusCode)
	}

	// A valid key still works, which is what makes the rejections meaningful.
	if resp, _ := h.apiDo(http.MethodGet, "/api/v1/me", real, nil, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("valid key status = %d, want 200", resp.StatusCode)
	}
}

func TestAPIRejectsExpiredKey(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")

	past := time.Now().Add(-time.Hour)
	key := h.seedKey(user.ID, "stale", &past)

	if resp, _ := h.apiDo(http.MethodGet, "/api/v1/me", key, nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expired key status = %d, want 401", resp.StatusCode)
	}
}

func TestAPIUploadLifecycle(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	resp, body := h.apiDo(http.MethodGet, "/api/v1/me", key, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d, want 200", resp.StatusCode)
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Username != "alice" {
		t.Errorf("me username = %q, want alice", me.Username)
	}

	// Upload with no CSRF token: the bearer token exempts the request.
	resp, raw := h.apiUpload(key, nil, map[string][]byte{
		"one.png": pngFixture(t, 120, 90),
		"two.png": pngFixture(t, 60, 60),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type = %q, want JSON", ct)
	}

	uploaded := decodeUpload(t, raw)
	if len(uploaded.Files) != 2 {
		t.Fatalf("uploaded %d files, want 2", len(uploaded.Files))
	}

	var file apiFileJSON
	for _, f := range uploaded.Files {
		if f.Name == "one.png" {
			file = f
		}
	}
	if file.ID == "" {
		t.Fatalf("one.png missing from the response: %+v", uploaded.Files)
	}
	if file.RawURL == "" || file.PageURL == "" || file.ThumbURL == "" {
		t.Fatalf("upload response is missing URLs: %+v", file)
	}
	if !strings.HasSuffix(file.RawURL, "/f/"+file.ID+"/raw") {
		t.Errorf("raw_url = %q", file.RawURL)
	}
	// The API follows the instance default like the web form, which on this
	// harness is members: a script that says nothing does not publish to the
	// world, but it does not have to repeat the instance policy either.
	if file.Public {
		t.Error("api upload defaulted to public")
	}
	if file.Visibility != "members" {
		t.Errorf("visibility = %q, want the instance default, members", file.Visibility)
	}
	if file.Kind != "image" {
		t.Errorf("kind = %q, want image", file.Kind)
	}
	if file.Width != 120 || file.Height != 90 {
		t.Errorf("dimensions = %dx%d, want 120x90", file.Width, file.Height)
	}

	resp, body = h.apiDo(http.MethodGet, "/api/v1/files/"+file.ID, key, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("file status = %d, want 200", resp.StatusCode)
	}
	var fetched apiFileJSON
	if err := json.Unmarshal(body, &fetched); err != nil {
		t.Fatalf("decode file: %v", err)
	}
	if fetched.ID != file.ID || fetched.Width != 120 {
		t.Errorf("fetched = %+v, want id %s and width 120", fetched, file.ID)
	}

	resp, body = h.apiDo(http.MethodGet, "/api/v1/files?limit=10", key, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	var list struct {
		Total int `json:"total"`
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 2 || len(list.Files) != 2 {
		t.Errorf("list = %d total / %d files, want 2 and 2", list.Total, len(list.Files))
	}

	// The bytes are served, and a bearer token grants access to the owner's own
	// private upload even on the public /f/ routes.
	if resp, _ := h.apiDo(http.MethodGet, "/f/"+file.ID+"/raw", key, nil, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("raw status = %d, want 200", resp.StatusCode)
	}

	resp, _ = h.apiDo(http.MethodDelete, "/api/v1/files/"+file.ID, key, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", resp.StatusCode)
	}
	if resp, _ := h.apiDo(http.MethodGet, "/api/v1/files/"+file.ID, key, nil, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("status after delete = %d, want 404", resp.StatusCode)
	}
}

func TestAPIUploadOptions(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	_, raw := h.apiUpload(key, map[string]string{"public": "1"}, map[string][]byte{
		"shared.png": pngFixture(t, 40, 40),
	})
	shared := decodeUpload(t, raw)
	if len(shared.Files) != 1 || !shared.Files[0].Public {
		t.Fatalf("public flag was not honoured: %+v", shared.Files)
	}

	anon := &http.Client{}
	anonResp, err := anon.Get(h.server.URL + "/f/" + shared.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	anonResp.Body.Close()
	if anonResp.StatusCode != http.StatusOK {
		t.Errorf("anonymous access to a public upload = %d, want 200", anonResp.StatusCode)
	}

	// ?format=text returns a bare URL, which is what ShareX expects.
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("files", "text.png")
	part.Write(pngFixture(t, 30, 30))
	writer.Close()

	resp, raw := h.apiDo(http.MethodPost, "/api/v1/upload?format=text", key,
		strings.NewReader(body.String()), writer.FormDataContentType())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("text upload status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content type = %q, want text/plain", ct)
	}
	line := strings.TrimSpace(string(raw))
	if !strings.Contains(line, "/f/") || strings.ContainsAny(line, "{}") {
		t.Errorf("text response = %q, want a bare URL", line)
	}
}

func TestAPIUploadRejectsNonImage(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	resp, raw := h.apiUpload(key, nil, map[string][]byte{
		"notes.txt": []byte("this is definitely not an image"),
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}
	if !strings.Contains(string(raw), "notes.txt") {
		t.Errorf("error does not name the offending file: %s", truncate(string(raw)))
	}
}

func TestAPIKeyCannotReachAnotherUsersFiles(t *testing.T) {
	h := newHarness(t)

	alice := h.seedUser("alice")
	bob := h.seedUser("bob")
	aliceKey := h.seedKey(alice.ID, "alice", nil)
	bobKey := h.seedKey(bob.ID, "bob", nil)

	_, raw := h.apiUpload(aliceKey, nil, map[string][]byte{
		"private.png": pngFixture(t, 50, 50),
	})
	uploaded := decodeUpload(t, raw)
	if len(uploaded.Files) != 1 {
		t.Fatalf("setup upload failed: %s", truncate(string(raw)))
	}
	id := uploaded.Files[0].ID

	if resp, _ := h.apiDo(http.MethodGet, "/api/v1/files/"+id, bobKey, nil, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("bob reading alice's file = %d, want 404", resp.StatusCode)
	}
	if resp, _ := h.apiDo(http.MethodDelete, "/api/v1/files/"+id, bobKey, nil, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("bob deleting alice's file = %d, want 404", resp.StatusCode)
	}
	if resp, _ := h.apiDo(http.MethodGet, "/api/v1/files/"+id, aliceKey, nil, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("alice reading her own file = %d, want 200", resp.StatusCode)
	}
}

func TestAPIUploadIntoAlbum(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	album, err := h.store.CreateAlbum(h.t.Context(), user.ID, "Summer 2026", "", models.VisibilityPublic, models.AlbumAccessOwner)
	if err != nil {
		t.Fatalf("create album: %v", err)
	}

	_, raw := h.apiUpload(key, map[string]string{"album": album.Slug}, map[string][]byte{
		"beach.png": pngFixture(t, 80, 60),
	})
	uploaded := decodeUpload(t, raw)
	if len(uploaded.Files) != 1 {
		t.Fatalf("upload failed: %s", truncate(string(raw)))
	}

	albumID := album.ID
	files, err := h.store.ListFiles(h.t.Context(), store.FileQuery{AlbumID: &albumID, Limit: 10})
	if err != nil {
		t.Fatalf("list album files: %v", err)
	}
	if len(files) != 1 || files[0].ID != uploaded.Files[0].ID {
		t.Errorf("album contains %d files, want just the upload", len(files))
	}
}

func TestAPIKeyLastUsedIsRecorded(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	h.apiDo(http.MethodGet, "/api/v1/me", key, nil, "")

	keys, err := h.store.APIKeysByUser(h.t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Fatal("last_used_at was not recorded after an authenticated call")
	}
}

func TestAPIKeyManagementPage(t *testing.T) {
	h := newHarness(t)

	h.get("/")
	h.postForm("/register", url.Values{
		"csrf_token": {h.csrf()},
		"username":   {"alice"},
		"password":   {"hunter2hunter2"},
	})

	resp, page := h.get("/settings/api-keys")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api keys page = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(page, "No API keys yet") {
		t.Error("empty state is missing")
	}

	// Creating a key reveals it exactly once.
	resp, panel := h.postForm("/settings/api-keys", url.Values{
		"csrf_token": {h.csrf()},
		"name":       {"laptop"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create key = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(panel, "stored hashed") {
		t.Error("the reveal notice is missing")
	}

	secret := extractKey(panel)
	if secret == "" {
		t.Fatalf("no key in the response: %s", truncate(panel))
	}
	if _, ok := apikeys.Split(secret); !ok {
		t.Errorf("revealed key %q is malformed", secret)
	}

	if resp, _ := h.apiDo(http.MethodGet, "/api/v1/me", secret, nil, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("the newly created key does not authenticate: %d", resp.StatusCode)
	}
	if _, rendered := h.get("/settings/api-keys"); strings.Contains(rendered, secret) {
		t.Error("the full key is still being rendered after creation")
	}

	// Revoking it takes effect immediately.
	user, err := h.store.UserByUsername(h.t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := h.store.APIKeysByUser(h.t.Context(), user.ID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("expected one stored key, got %d (%v)", len(keys), err)
	}

	h.postForm("/settings/api-keys/"+strconv.FormatInt(keys[0].ID, 10)+"/delete", url.Values{
		"csrf_token": {h.csrf()},
	})

	if resp, _ := h.apiDo(http.MethodGet, "/api/v1/me", secret, nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked key status = %d, want 401", resp.StatusCode)
	}
}
