// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/config"
	"imvault/internal/models"
)

func TestAPIKeysCannotAuthenticateWebRoutes(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("admin")
	if err := h.store.SetUserRole(t.Context(), u.ID, models.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	key := h.seedKey(u.ID, "uploader", nil)
	for _, path := range []string{"/admin/users", "/settings/api-keys", "/settings/account/export"} {
		resp, _ := h.apiDo(http.MethodGet, path, key, nil, "")
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("API key accessing %s = %d, want login redirect", path, resp.StatusCode)
		}
	}
	resp, _ := h.apiDo(http.MethodPost, "/settings/api-keys", key,
		strings.NewReader("name=unauthorized"), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("API key creating another key without CSRF = %d, want 403", resp.StatusCode)
	}
}

type auditCountingReader struct {
	io.Reader
	read int
}

func (r *auditCountingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}

func auditMultipart(t *testing.T, size int) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField(csrfField, strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	f, err := w.CreateFormFile("files", "test.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("x"), size)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), w.FormDataContentType()
}

func TestSecondFactorRateLimitIgnoresSuppliedUsername(t *testing.T) {
	h := newHarnessWith(t, func(c *config.Config) {
		c.LoginRatePerHour = 60
		c.LoginBurst = 3
	})
	h.get("/")
	for i := 0; i < 4; i++ {
		resp, _ := h.postForm("/login/2fa", url.Values{
			csrfField:  {h.csrf()},
			"username": {fmt.Sprintf("unused-%d", i)},
			"code":     {"000000"},
		})
		if i == 3 && resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("rotating irrelevant usernames bypassed 2FA rate limit: %d", resp.StatusCode)
		}
	}
}

func TestAnonymousUploadCannotExceedInstanceCeiling(t *testing.T) {
	h := newHarness(t)
	h.setPolicy(t, models.Settings{AllowAnonymousUploads: true,
		DefaultVisibility: models.VisibilityMembers, MaxTotalBytes: 1})
	h.get("/upload")
	_, body := h.uploadFiles(map[string]string{}, []uploadFile{{name: "anon.png", data: pngFixture(t, 16, 16)}})
	if countStoredFiles(t, h) != 0 || !strings.Contains(body, "instance is full") {
		t.Fatalf("anonymous upload bypassed ceiling: %s", truncate(body))
	}
}
