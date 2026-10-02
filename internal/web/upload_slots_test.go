// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"imvault/internal/config"
)

// tricklingUpload starts a multipart POST that never finishes, the way a
// client holding a connection open would, and returns a function that ends it.
func tricklingUpload(t *testing.T, h *harness, header http.Header) func() {
	t.Helper()
	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/upload", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	if _, err := pw.Write([]byte("--xyz\r\nContent-Disposition: form-data; name=\"files\"; filename=\"a.png\"\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	// Give the server time to take whatever it is going to take.
	time.Sleep(200 * time.Millisecond)
	return func() {
		_ = pw.CloseWithError(io.ErrUnexpectedEOF)
		<-done
	}
}

// An anonymous request with no token must not be able to hold an upload slot,
// whatever the policy on anonymous uploads.
func TestTokenlessUploadsCannotHoldTheSlots(t *testing.T) {
	for _, anonymous := range []bool{false, true} {
		t.Run(fmt.Sprintf("anonymous uploads %v", anonymous), func(t *testing.T) {
			h := newHarnessWith(t, func(c *config.Config) {
				c.MaxConcurrentUploads = 1
				c.AllowAnonymousUploads = anonymous
			})
			h.srv.processing.wait = 300 * time.Millisecond
			alice := h.seedUser("alice")

			stop := tricklingUpload(t, h, http.Header{})
			defer stop()

			resp, body := h.sessionFor(t, alice.ID).upload(map[string]string{}, []uploadFile{
				{name: "x.png", data: pngFixture(t, 8, 8)},
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("a signed-in upload was refused while a tokenless request waited: %d %s",
					resp.StatusCode, truncate(body))
			}
		})
	}
}

// A request with no token is refused having read no more than the first part
// of its body, so an oversized tokenless upload costs nothing.
func TestTokenlessMultipartReadsOnlyTheFirstPart(t *testing.T) {
	h := newHarness(t)
	body, contentType := auditMultipartWithout(t, 4<<20)
	for _, path := range []string{"/upload", "/login", "/admin/settings/branding-assets"} {
		counter := &auditCountingReader{Reader: bytes.NewReader(body)}
		req := httptest.NewRequest(http.MethodPost, path, counter)
		req.Header.Set("Content-Type", contentType)
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: strings.Repeat("a", 32)})
		w := httptest.NewRecorder()
		h.srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s tokenless multipart = %d, want 403", path, w.Code)
		}
		if counter.read > csrfPeekLimit {
			t.Errorf("%s read %d bytes looking for a token, more than %d", path, counter.read, csrfPeekLimit)
		}
	}
}

// auditMultipartWithout is a large multipart body whose first part is a file,
// not a token.
func auditMultipartWithout(t *testing.T, size int) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("files", "big.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), size)); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField(csrfField, strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), w.FormDataContentType()
}

// With every slot taken, an upload that carries its token in the header is
// refused before a byte of its body is read.
func TestBusyUploadDoesNotReadTheBody(t *testing.T) {
	h := newHarness(t)
	h.srv.processing.wait = time.Millisecond
	release, ok := h.srv.processing.acquire(t.Context())
	if !ok {
		t.Fatal("could not occupy upload slot")
	}
	defer release()
	body, contentType := auditMultipart(t, 1024)
	counter := &auditCountingReader{Reader: bytes.NewReader(body)}
	req := httptest.NewRequest(http.MethodPost, "/upload", counter)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(csrfHeader, strings.Repeat("a", 32))
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: strings.Repeat("a", 32)})
	w := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable || counter.read != 0 {
		t.Fatalf("busy upload: status=%d, bytes read=%d; want 503 without reading", w.Code, counter.read)
	}
}

// An oversized body with a valid token is refused as too large, after reading
// no more than the limit.
func TestOversizedUploadIsRefusedAsTooLarge(t *testing.T) {
	h := newHarnessWith(t, func(c *config.Config) {
		c.MaxUploadBytes = 1
		c.MaxVideoBytes = 1
	})
	body, contentType := auditMultipart(t, 2<<20)
	counter := &auditCountingReader{Reader: bytes.NewReader(body)}
	req := httptest.NewRequest(http.MethodPost, "/upload", counter)
	req.Header.Set("Content-Type", contentType)
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: strings.Repeat("a", 32)})
	w := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized upload = %d, want 413", w.Code)
	}
	if counter.read >= len(body) {
		t.Error("the whole oversized body was read")
	}
}

// postNoJS submits the uploader form as a browser without JavaScript would:
// the token is the first field, there is no header, and no HX-Request.
func postNoJS(t *testing.T, s *session, fields url.Values, name string, data []byte) (*http.Response, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField(csrfField, s.token()); err != nil {
		t.Fatal(err)
	}
	for key, values := range fields {
		for _, value := range values {
			if err := w.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	part, err := w.CreateFormFile("files", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, s.h.server.URL+"/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBody(t, resp)
	return resp, readAll(t, resp.Body)
}

// The uploader works without JavaScript: the token rides in the first field,
// the answer is a whole page, and the album menu is honoured.
func TestUploadWorksWithoutJavaScript(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	s := h.sessionFor(t, alice.ID)
	if resp, _ := s.post("/albums", url.Values{"title": {"Trip"}, "visibility": {"members"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create album = %d", resp.StatusCode)
	}
	album, err := h.store.AlbumBySlug(t.Context(), "trip")
	if err != nil {
		t.Fatal(err)
	}

	resp, page := postNoJS(t, s, url.Values{"album_id": {itoa64(album.ID)}, "visibility": {"members"}},
		"nojs.png", pngFixture(t, 16, 16))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no-JS upload = %d: %s", resp.StatusCode, truncate(page))
	}
	if !strings.Contains(page, "<html") || !strings.Contains(page, `id="results"`) {
		t.Error("a no-JS upload did not get a whole page back")
	}
	// The page has its own file input, so read the id from the results only.
	id := firstFileID(t, page[strings.Index(page, `id="results"`):])
	members, err := h.store.AlbumMembership(t.Context(), album.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !members[id] {
		t.Errorf("the album chosen in the uploader form was ignored: %s not in %v", id, members)
	}
}

// The htmx uploader carries the token in a header and gets a fragment.
func TestUploadWorksWithJavaScript(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	resp, body := h.sessionFor(t, alice.ID).upload(map[string]string{}, []uploadFile{
		{name: "js.png", data: pngFixture(t, 16, 16)},
	})
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "<html") {
		t.Fatalf("htmx upload = %d, whole page = %v", resp.StatusCode, strings.Contains(body, "<html"))
	}
	if firstFileID(t, body) == "" {
		t.Error("no file was created")
	}
}

// A body that stops arriving is cut off at the read deadline rather than
// holding the connection for as long as the client likes.
func TestStalledBodiesHitTheReadDeadline(t *testing.T) {
	h := newHarness(t)
	h.srv.formReadTimeout = 300 * time.Millisecond

	address := strings.TrimPrefix(h.server.URL, "http://")
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	request := "POST /login HTTP/1.1\r\nHost: " + address + "\r\n" +
		"Content-Type: application/x-www-form-urlencoded\r\nContent-Length: 1000\r\n\r\nusername=a"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err == nil {
		_ = resp.Body.Close()
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("a stalled body held the connection for %s", elapsed)
	}
}

// A form posted as multipart to an ordinary route still reaches its handler
// with its fields, once its token has been checked.
func TestMultipartFormsReachOrdinaryHandlers(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	s := h.sessionFor(t, alice.ID)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, field := range [][2]string{{csrfField, s.token()}, {"title", "Multipart Album"}, {"visibility", "members"}} {
		if err := w.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/albums", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	closeBody(t, resp)
	if _, err := h.store.AlbumBySlug(t.Context(), "multipart-album"); err != nil {
		t.Fatalf("a multipart form lost its fields: %d (%v)", resp.StatusCode, err)
	}
}
