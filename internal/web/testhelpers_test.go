// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"io"
	"io/fs"
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
