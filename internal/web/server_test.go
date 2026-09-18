// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/mail"
	"imvault/internal/media"
	"imvault/internal/storage"
	"imvault/internal/store"
)

type harness struct {
	t         *testing.T
	server    *httptest.Server
	client    *http.Client
	jar       *cookiejar.Jar
	store     *store.Store
	processor *media.Processor
	srv       *Server
	mailer    *captureMail
	dataDir   string
}

// mailMode selects how the harness wires its mail sender.
type mailMode int

const (
	// mailOff is an instance with no relay configured.
	mailOff mailMode = iota
	// mailDirect hands messages straight to the capturing sender.
	mailDirect
	// mailQueued puts the durable queue in front of the capturing sender, which
	// is what a configured instance actually runs.
	mailQueued
)

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessFull(t, nil, mailOff)
}

// newHarnessWith builds a harness, letting a test adjust the configuration
// before the server is constructed.
func newHarnessWith(t *testing.T, mutate func(*config.Config)) *harness {
	t.Helper()
	return newHarnessFull(t, mutate, mailOff)
}

// newMailHarness is a harness whose mail sender accepts messages, so the reset
// and verification flows can be inspected end to end.
func newMailHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessFull(t, nil, mailDirect)
}

// newQueueHarness wires the durable outbound queue, which is what a real
// instance with a relay configured runs.
func newQueueHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessFull(t, nil, mailQueued)
}

// newHarnessFull assembles a server with a capturing mail sender.
func newHarnessFull(t *testing.T, mutate func(*config.Config), mode mailMode) *harness {
	t.Helper()

	dir := t.TempDir()
	cfg := &config.Config{
		Addr:                  "127.0.0.1:0",
		DataDir:               dir,
		DBPath:                filepath.Join(dir, "test.db"),
		AllowSignup:           true,
		AllowAnonymousUploads: true,
		AnonymousTTL:          time.Hour,
		SessionTTL:            time.Hour,
		CleanupInterval:       time.Hour,
		MaxUploadBytes:        8 << 20,
		MaxVideoBytes:         16 << 20,
		MaxVideoDuration:      30 * time.Second,
		FFmpegPath:            "ffmpeg",
		FFprobePath:           "ffprobe",
		ThumbMax:              128,
		PreviewMax:            256,
		JPEGQuality:           80,
		// Mirror the defaults config.Load would apply: a zero TTL would mint
		// tokens that are already expired.
		PasswordResetTTL:  time.Hour,
		EmailVerifyTTL:    24 * time.Hour,
		MailMaxAttempts:   3,
		MailRetryInterval: time.Minute,
	}
	if mutate != nil {
		mutate(cfg)
	}

	ctx := t.Context()
	database, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	objects, err := storage.NewDisk(filepath.Join(dir, "objects"))
	if err != nil {
		t.Fatalf("storage: %v", err)
	}

	st := store.New(database)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	processor := media.NewProcessor(
		cfg.ThumbMax, cfg.PreviewMax, cfg.JPEGQuality, cfg.MaxVideoDuration,
		media.NewFFmpeg(cfg.FFmpegPath, cfg.FFprobePath),
	)

	mailer := &captureMail{enabled: mode != mailOff}

	var sender mail.Sender = mailer
	if mode == mailQueued {
		sender = NewMailQueue(st, mailer, cfg.MailMaxAttempts, cfg.MailRetryInterval, logger)
	}

	srv, err := New(cfg, st, objects, processor, sender, logger)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &harness{
		t:         t,
		server:    ts,
		store:     st,
		processor: processor,
		srv:       srv,
		mailer:    mailer,
		dataDir:   dir,
		jar:       jar,
		client: &http.Client{
			Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// csrf returns the double-submit token issued by the server.
func (h *harness) csrf() string {
	h.t.Helper()
	u, _ := url.Parse(h.server.URL)
	for _, c := range h.jar.Cookies(u) {
		if c.Name == csrfCookie {
			return c.Value
		}
	}
	h.t.Fatal("no CSRF cookie present")
	return ""
}

func (h *harness) get(path string) (*http.Response, string) {
	h.t.Helper()
	resp, err := h.client.Get(h.server.URL + path)
	if err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read %s: %v", path, err)
	}
	return resp, string(body)
}

func (h *harness) postForm(path string, form url.Values) (*http.Response, string) {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.server.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		h.t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(csrfHeader, h.csrf())

	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, string(out)
}

// upload posts a multipart body containing the given files.
func (h *harness) upload(names ...string) (*http.Response, string) {
	h.t.Helper()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	if err := mw.WriteField("csrf_token", h.csrf()); err != nil {
		h.t.Fatal(err)
	}
	if err := mw.WriteField("public", "1"); err != nil {
		h.t.Fatal(err)
	}
	for _, name := range names {
		part, err := mw.CreateFormFile("files", name)
		if err != nil {
			h.t.Fatal(err)
		}
		if _, err := part.Write(pngFixture(h.t, 200, 150)); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		h.t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/upload", &body)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(csrfHeader, h.csrf())
	req.Header.Set("HX-Request", "true")

	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, string(out)
}

var fileIDRe = regexp.MustCompile(`id="file-([a-z0-9]+)"`)

func pngFixture(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestAnonymousBrowsingRendersPublicPages checks that every page reachable
// without an account renders without a template error.
func TestAnonymousBrowsingRendersPublicPages(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/", "/login", "/register", "/upload", "/tags"} {
		resp, body := h.get(path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		if !strings.Contains(body, "</html>") {
			t.Errorf("GET %s did not render a full document", path)
		}
	}

	resp, body := h.get("/f/nope")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /f/nope = %d, want 404", resp.StatusCode)
	}
	if !strings.Contains(body, "404") {
		t.Error("404 page missing its code")
	}
}

func TestRegisterUploadAndBrowse(t *testing.T) {
	h := newHarness(t)

	// Establish a session for the CSRF cookie.
	h.get("/")
	token := h.csrf()

	resp, _ := h.postForm("/register", url.Values{
		"csrf_token": {token},
		"username":   {"marcus"},
		"password":   {"hunter2hunter2"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("register = %d, want 303", resp.StatusCode)
	}

	// Upload two images in one request.
	resp, body := h.upload("one.png", "two.png")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d, want 200", resp.StatusCode)
	}
	ids := fileIDRe.FindAllStringSubmatch(body, -1)
	if len(ids) != 2 {
		t.Fatalf("upload returned %d cards, want 2 (body: %s)", len(ids), truncate(body))
	}
	id := ids[0][1]

	// The rendered file page should mention the stored filename.
	resp, page := h.get("/f/" + id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("file page = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(page, "one.png") {
		t.Errorf("file page does not show the original name")
	}

	// Renditions are served with an image content type.
	for _, kind := range []string{"raw", "thumb", "preview"} {
		resp, _ := h.get("/f/" + id + "/" + kind)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s = %d, want 200", kind, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
			t.Errorf("%s content-type = %q, want an image type", kind, ct)
		}
	}

	// Gallery and the listing pages render.
	resp, gallery := h.get("/gallery")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gallery = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(gallery, "one.png") {
		t.Error("gallery does not list the uploaded file")
	}

	// Tagging round-trips through the HTMX fragment.
	resp, chips := h.postForm("/f/"+id+"/tags", url.Values{
		"csrf_token": {h.csrf()},
		"name":       {"Holiday Snaps"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add tag = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(chips, "Holiday Snaps") {
		t.Errorf("tag fragment missing the new tag: %s", truncate(chips))
	}
	if _, tagPage := h.get("/tags/marcus/holiday-snaps"); !strings.Contains(tagPage, "one.png") {
		t.Error("tag page does not list the tagged file")
	}

	// Albums.
	resp, _ = h.postForm("/albums", url.Values{
		"csrf_token":  {h.csrf()},
		"title":       {"Summer 2026"},
		"description": {"Warm"},
		"public":      {"1"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create album = %d, want 303", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if location != "/a/summer-2026" {
		t.Fatalf("album location = %q, want /a/summer-2026", location)
	}

	resp, _ = h.postForm("/a/summer-2026/files", url.Values{
		"csrf_token": {h.csrf()},
		"files":      {id},
	})
	// Without an HX-Request header the handler redirects rather than replying 204.
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("add to album = %d, want 303", resp.StatusCode)
	}

	resp, album := h.get("/a/summer-2026")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("album page = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(album, "file-"+id) {
		t.Error("album does not contain the added file")
	}
}

func TestCSRFIsEnforced(t *testing.T) {
	h := newHarness(t)
	h.get("/")

	// A POST without the token header must be rejected.
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/register",
		strings.NewReader("username=x&password=y"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST without CSRF = %d, want 403", resp.StatusCode)
	}
}

func TestAnonymousUploadsArePublicAndExpire(t *testing.T) {
	h := newHarness(t)

	h.get("/upload")
	resp, body := h.upload("anon.png")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous upload = %d, want 200", resp.StatusCode)
	}

	ids := fileIDRe.FindAllStringSubmatch(body, -1)
	if len(ids) != 1 {
		t.Fatalf("got %d cards, want 1", len(ids))
	}
	id := ids[0][1]

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatalf("load file: %v", err)
	}
	if file.UserID != nil {
		t.Error("anonymous upload should have no owner")
	}
	if !file.IsPublic {
		t.Error("anonymous upload should be public")
	}
	if file.ExpiresAt == nil {
		t.Fatal("anonymous upload should carry an expiry")
	}
	if time.Until(*file.ExpiresAt) > time.Hour+time.Minute {
		t.Errorf("expiry %s is beyond the configured TTL", file.ExpiresAt)
	}
}

func TestPrivateFilesAreHiddenFromAnonymous(t *testing.T) {
	h := newHarness(t)
	h.get("/")
	h.postForm("/register", url.Values{
		"csrf_token": {h.csrf()},
		"username":   {"marcus"},
		"password":   {"hunter2hunter2"},
	})

	// Upload privately by omitting the public field.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf_token", h.csrf())
	part, _ := mw.CreateFormFile("files", "secret.png")
	part.Write(pngFixture(t, 64, 64))
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, h.server.URL+"/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(csrfHeader, h.csrf())
	req.Header.Set("HX-Request", "true")
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	ids := fileIDRe.FindAllStringSubmatch(string(out), -1)
	if len(ids) != 1 {
		t.Fatalf("upload failed: %s", truncate(string(out)))
	}
	id := ids[0][1]

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if file.IsPublic {
		t.Fatal("upload without the public flag should be private")
	}

	// A fresh anonymous client must not see it.
	anonJar, _ := cookiejar.New(nil)
	anon := &http.Client{Jar: anonJar}
	anonResp, err := anon.Get(h.server.URL + "/f/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer anonResp.Body.Close()
	if anonResp.StatusCode != http.StatusNotFound {
		t.Errorf("anonymous access to a private file = %d, want 404", anonResp.StatusCode)
	}
}

func truncate(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}
