// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"imvault/internal/ids"
	"imvault/internal/mail"
)

// newJar returns an empty cookie jar.
func newJar() (*cookiejar.Jar, error) { return cookiejar.New(nil) }

// signIn returns a client carrying a session for an existing account, without
// going through the login form. It is how tests act as a seeded user whose
// password they do not know.
func (h *harness) signIn(t *testing.T, userID int64) *http.Client {
	t.Helper()

	token := ids.Token(32)
	if err := h.store.CreateSession(t.Context(), hashToken(token), userID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}

	jar, err := newJar()
	if err != nil {
		t.Fatalf("jar: %v", err)
	}

	parsed, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	jar.SetCookies(parsed, []*http.Cookie{{
		Name:  sessionCookie,
		Value: token,
		Path:  "/",
	}})

	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// csrfFor primes a client with a CSRF cookie and returns the token.
func (h *harness) csrfFor(t *testing.T, client *http.Client) string {
	t.Helper()

	resp, err := client.Get(h.server.URL + "/")
	if err != nil {
		t.Fatalf("prime client: %v", err)
	}
	resp.Body.Close()

	parsed, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	for _, cookie := range client.Jar.Cookies(parsed) {
		if cookie.Name == csrfCookie {
			return cookie.Value
		}
	}
	t.Fatal("no CSRF cookie present")
	return ""
}

// doForm posts a form as an arbitrary client.
func doForm(t *testing.T, client *http.Client, base, path string, form url.Values) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	resp.Body.Close()
	return resp
}

// captureMail records messages instead of sending them, so a test can follow a
// reset link the way a recipient would.
type captureMail struct {
	mu       sync.Mutex
	messages []mail.Message
	enabled  bool
	// failWith makes Send report an error, standing in for an unreachable relay.
	failWith error
}

func (c *captureMail) Enabled() bool { return c.enabled }

func (c *captureMail) Send(_ context.Context, msg mail.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failWith != nil {
		return c.failWith
	}
	c.messages = append(c.messages, msg)
	return nil
}

// errFakeMail stands in for an unreachable relay.
var errFakeMail = errors.New("mail: relay unreachable")

// passwordMatches reports whether a password verifies against a stored hash.
func passwordMatches(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// mustHashPassword hashes a password for a test fixture.
func mustHashPassword(t *testing.T, password string) string {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return string(hash)
}

var resetLinkRe = regexp.MustCompile(`value="[^"]*` + resetPath + `([A-Za-z0-9]+)"`)

// extractResetLink pulls the one-time reset link out of the admin page.
func extractResetLink(t *testing.T, page string) string {
	t.Helper()

	match := resetLinkRe.FindStringSubmatch(page)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

// sent returns a copy of everything captured so far.
func (c *captureMail) sent() []mail.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]mail.Message(nil), c.messages...)
}

// last returns the most recent message, failing if there is none.
func (c *captureMail) last(t *testing.T) mail.Message {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		t.Fatal("no mail was sent")
	}
	return c.messages[len(c.messages)-1]
}

// resetTokenFrom extracts the token from the reset link in a message body.
func resetTokenFrom(t *testing.T, msg mail.Message) string {
	t.Helper()

	for _, field := range strings.Fields(msg.Body) {
		if i := strings.Index(field, resetPath); i >= 0 {
			return strings.TrimSpace(field[i+len(resetPath):])
		}
	}
	t.Fatalf("no reset link in the message body:\n%s", msg.Body)
	return ""
}

// verifyTokenFrom extracts the token from the confirmation link in a body.
func verifyTokenFrom(t *testing.T, msg mail.Message) string {
	t.Helper()

	for _, field := range strings.Fields(msg.Body) {
		if i := strings.Index(field, verifyPath); i >= 0 {
			return strings.TrimSpace(field[i+len(verifyPath):])
		}
	}
	t.Fatalf("no verification link in the message body:\n%s", msg.Body)
	return ""
}

// readAll drains a response body, failing the test on error.
func readAll(t *testing.T, r io.Reader) string {
	t.Helper()

	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

// countStoredObjects counts the files in the harness's object store, which is
// where the bytes live.
func countStoredObjects(t *testing.T, dataDir string) int {
	t.Helper()

	root := filepath.Join(dataDir, "objects")
	var count int

	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			count++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk %s: %v", root, err)
	}
	return count
}

// itoa64 renders an id for use in a request path.
func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

// session is an independent browser: its own cookie jar, so that a question
// like "is this signed in?" has an answer that means something. The harness
// client is signed in by registerForm, which silently made several sign-in
// assertions pass for the wrong reason.
type session struct {
	t      *testing.T
	h      *harness
	client *http.Client
	jar    *cookiejar.Jar
	csrf   string
}

// newSession starts a browser with no session, primed with a CSRF token.
func (h *harness) newSession(t *testing.T) *session {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}

	s := &session{
		t:   t,
		h:   h,
		jar: jar,
		client: &http.Client{
			Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}

	resp, err := s.client.Get(h.server.URL + "/")
	if err != nil {
		t.Fatalf("prime session: %v", err)
	}
	resp.Body.Close()

	parsed, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	for _, cookie := range jar.Cookies(parsed) {
		if cookie.Name == csrfCookie {
			s.csrf = cookie.Value
		}
	}
	if s.csrf == "" {
		t.Fatal("no CSRF cookie was issued to the new session")
	}
	return s
}

func (s *session) get(path string) (*http.Response, string) {
	s.t.Helper()

	resp, err := s.client.Get(s.h.server.URL + path)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	return resp, readAll(s.t, resp.Body)
}

func (s *session) post(path string, form url.Values) (*http.Response, string) {
	s.t.Helper()

	form.Set("csrf_token", s.csrf)

	req, err := http.NewRequest(http.MethodPost, s.h.server.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		s.t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	return resp, readAll(s.t, resp.Body)
}

// signedIn reports whether the session can reach an authenticated page.
func (s *session) signedIn() bool {
	s.t.Helper()

	resp, _ := s.get("/gallery")
	return resp.StatusCode == http.StatusOK
}

// signInTo walks the two sign-in steps, code included.
func (s *session) signInTo(username, password, code string) (*http.Response, string) {
	s.t.Helper()

	resp, body := s.post("/login", url.Values{
		"username": {username},
		"password": {password},
	})
	if resp.StatusCode == http.StatusSeeOther && resp.Header.Get("Location") == "/login/2fa" {
		if code == "" {
			return resp, body
		}
		return s.post("/login/2fa", url.Values{"code": {code}})
	}
	return resp, body
}

// sessionFor returns a session for an existing account, creating the session
// row directly. It is how a test acts as somebody who is not the first account,
// and therefore not the administrator.
func (h *harness) sessionFor(t *testing.T, userID int64) *session {
	t.Helper()

	token := ids.Token(32)
	if err := h.store.CreateSession(t.Context(), hashToken(token), userID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}

	s := &session{
		t:   t,
		h:   h,
		jar: jar,
		client: &http.Client{
			Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}

	parsed, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}

	// Seed both the session and a CSRF cookie, as a browser would have.
	jar.SetCookies(parsed, []*http.Cookie{
		{Name: sessionCookie, Value: token, Path: "/"},
		{Name: csrfCookie, Value: ids.Token(32), Path: "/"},
	})
	for _, cookie := range jar.Cookies(parsed) {
		if cookie.Name == csrfCookie {
			s.csrf = cookie.Value
		}
	}
	return s
}

// upload posts files as this session, the way the uploader page does.
func (s *session) upload(fields map[string]string, files []uploadFile) (*http.Response, string) {
	s.t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	fields["csrf_token"] = s.csrf
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			s.t.Fatal(err)
		}
	}
	for _, file := range files {
		part, err := writer.CreateFormFile("files", file.name)
		if err != nil {
			s.t.Fatal(err)
		}
		if _, err := part.Write(file.data); err != nil {
			s.t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		s.t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, s.h.server.URL+"/upload", &body)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-CSRF-Token", s.csrf)
	req.Header.Set("HX-Request", "true")

	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	return resp, readAll(s.t, resp.Body)
}

// statModTime reports a file's modification time, for checks that an object was
// left alone.
func statModTime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// getBytes fetches a path and returns the raw body, for downloads.
func (s *session) getBytes(path string) (*http.Response, []byte) {
	s.t.Helper()

	resp, err := s.client.Get(s.h.server.URL + path)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatalf("read %s: %v", path, err)
	}
	return resp, body
}
