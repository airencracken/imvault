// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/config"
	"imvault/internal/models"
)

func TestAdminCanWhiteboxInstanceAndSetFooterSource(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.SourceURL = "https://github.com/airencracken/imvault"
	})
	guest := h.newSession(t)
	_, page := guest.get("/")
	if !strings.Contains(page, `href="https://github.com/airencracken/imvault"`) {
		t.Fatal("default footer does not link to the project source")
	}

	admin := h.provisionAdmin("boss")
	session := h.sessionFor(t, admin.ID)
	form := url.Values{
		"site_name":          {"Friends' Album"},
		"source_url":         {"https://example.org/source"},
		"welcome_title":      {"<script>stay text</script>"},
		"welcome_text":       {"Welcome to our group."},
		"allow_signup":       {"1"},
		"anonymous_ttl":      {"1h"},
		"default_visibility": {string(models.VisibilityMembers)},
	}
	resp, body := session.post("/admin/settings", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save branding = %d: %s", resp.StatusCode, truncate(body))
	}
	_, page = guest.get("/")
	for _, text := range []string{
		"<title>Friends&#39; Album</title>",
		`href="https://example.org/source"`,
		"&lt;script&gt;stay text&lt;/script&gt;",
		"Welcome to our group.",
	} {
		if !strings.Contains(page, text) {
			t.Errorf("customized home page missing %q", text)
		}
	}

	form.Set("source_url", "")
	resp, body = session.post("/admin/settings", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("clear footer source = %d: %s", resp.StatusCode, truncate(body))
	}
	_, page = guest.get("/")
	if strings.Contains(page, "example.org/source") || strings.Contains(page, "github.com/airencracken/imvault") {
		t.Fatal("blank source setting did not hide the footer link")
	}
}

func TestAdminCanReplaceAndRemoveBrandImages(t *testing.T) {
	h := newHarness(t)
	admin := h.provisionAdmin("boss")
	session := h.sessionFor(t, admin.ID)
	data := brandingPNG(t)
	resp, body := postBrandingAssets(t, session, map[string]string{}, map[string][]byte{"mascot": data, "favicon": data})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save branding images = %d: %s", resp.StatusCode, truncate(body))
	}
	_, page := session.get("/admin/settings")
	if !strings.Contains(page, `src="/branding/mascot"`) || !strings.Contains(page, `href="/branding/favicon"`) {
		t.Fatal("custom images are not referenced from the layout")
	}
	for _, name := range []string{"mascot", "favicon"} {
		resp, err := session.client.Get(h.server.URL + "/branding/" + name)
		if err != nil {
			t.Fatal(err)
		}
		cfg, decodeErr := png.DecodeConfig(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || decodeErr != nil || cfg.Width != 24 || cfg.Height != 24 {
			t.Errorf("served %s: status=%d dimensions=%dx%d decode=%v", name, resp.StatusCode, cfg.Width, cfg.Height, decodeErr)
		}
	}

	member := h.seedUser("jules")
	memberSession := h.sessionFor(t, member.ID)
	resp, body = postBrandingAssets(t, memberSession, map[string]string{}, map[string][]byte{"mascot": data})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member branding upload = %d, want 403: %s", resp.StatusCode, truncate(body))
	}

	resp, body = postBrandingAssets(t, session, map[string]string{"remove_mascot": "1", "remove_favicon": "1"}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("remove branding images = %d: %s", resp.StatusCode, truncate(body))
	}
	for _, name := range []string{"mascot", "favicon"} {
		resp, _ := session.get("/branding/" + name)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("removed %s status = %d, want 404", name, resp.StatusCode)
		}
	}

	resp, body = postBrandingAssets(t, session, map[string]string{}, map[string][]byte{"mascot": []byte("<svg onload=alert(1)>")})
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "error=") {
		t.Fatalf("SVG branding upload = %d, location %q, body %s", resp.StatusCode, resp.Header.Get("Location"), truncate(body))
	}
	if _, err := h.store.BrandingAsset(t.Context(), "mascot"); err == nil {
		t.Fatal("rejected SVG was stored as a branding asset")
	}
}

func postBrandingAssets(t *testing.T, session *session, fields map[string]string, files map[string][]byte) (*http.Response, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields["csrf_token"] = session.csrf
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		part, err := writer.CreateFormFile(name, name+".png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, session.h.server.URL+"/admin/settings/branding-assets", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := session.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp, readAll(t, resp.Body)
}

func brandingPNG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 24, 24))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
