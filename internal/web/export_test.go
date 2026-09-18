// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// readExport downloads the account export and returns its entries.
func readExport(t *testing.T, s *session) (*http.Response, map[string][]byte, []byte) {
	t.Helper()

	resp, body := s.getBytes("/settings/account/export")

	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the export is not a readable zip: %v", err)
	}

	entries := map[string][]byte{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		content, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		entries[file.Name] = content
	}
	return resp, entries, body
}

func TestAccountExportContainsTheManifest(t *testing.T) {
	h := newHarness(t)
	me, _ := accountUnderTest(t, h)

	resp, body := me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "holiday.png", data: pngFixture(t, 40, 40)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d", resp.StatusCode)
	}
	id := firstFileID(t, body)

	me.post("/f/"+id+"/tags", url.Values{"name": {"summer"}})

	httpResp, entries, _ := readExport(t, me)

	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("export = %d, want 200", httpResp.StatusCode)
	}
	if ct := httpResp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content type = %q, want application/zip", ct)
	}
	if cd := httpResp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("content disposition = %q, want an attachment", cd)
	}

	raw, ok := entries["manifest.json"]
	if !ok {
		t.Fatalf("no manifest in the archive; entries: %v", keysOf(entries))
	}

	var manifest exportManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("the manifest is not valid JSON: %v", err)
	}

	if manifest.Account.Username != "marcus" {
		t.Errorf("manifest account = %q, want marcus", manifest.Account.Username)
	}
	if len(manifest.Files) != 1 {
		t.Fatalf("manifest lists %d files, want 1", len(manifest.Files))
	}

	file := manifest.Files[0]
	if file.Name != "holiday.png" {
		t.Errorf("file name = %q", file.Name)
	}
	if file.Width != 40 || file.Height != 40 {
		t.Errorf("dimensions = %dx%d, want 40x40", file.Width, file.Height)
	}
	if file.SHA256 == "" {
		t.Error("the manifest does not carry the content hash")
	}
	if len(file.Tags) != 1 || file.Tags[0].Name != "summer" {
		t.Errorf("tags = %+v, want summer", file.Tags)
	}
	if _, ok := entries[file.Path]; !ok {
		t.Errorf("the manifest points at %q, which is not in the archive: %v", file.Path, keysOf(entries))
	}
}

func TestAccountExportCarriesTheOriginalBytes(t *testing.T) {
	h := newHarness(t)
	me, _ := accountUnderTest(t, h)

	original := pngFixture(t, 48, 48)
	resp, _ := me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "photo.png", data: original},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d", resp.StatusCode)
	}

	_, entries, _ := readExport(t, me)

	var found bool
	for name, content := range entries {
		if name == "manifest.json" {
			continue
		}
		found = true
		if !bytes.Equal(content, original) {
			t.Errorf("%s is %d bytes but the original was %d", name, len(content), len(original))
		}
		// Each file gets its own directory, so two uploads of the same name
		// cannot collide.
		if !strings.HasPrefix(name, "files/") {
			t.Errorf("entry %q is not under files/", name)
		}
		if !strings.HasSuffix(name, "/photo.png") {
			t.Errorf("entry %q lost the original name", name)
		}
	}
	if !found {
		t.Error("the archive contains no files")
	}
}

func TestAccountExportNeverCarriesCredentials(t *testing.T) {
	h := newHarness(t)
	me, userID := accountUnderTest(t, h)

	// Give the account everything that must not leak.
	if err := h.store.SetEmail(t.Context(), userID, "marcus@example.com", true); err != nil {
		t.Fatal(err)
	}
	secret := h.enableTwoFactorFor(t, userID)
	if _, err := h.store.CreateAPIKey(t.Context(), userID, "laptop", "abcdef123456", "hash", nil); err != nil {
		t.Fatal(err)
	}

	user, err := h.store.UserByID(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}

	_, entries, whole := readExport(t, me)

	// The archive as a whole, and the manifest in particular, must not contain
	// any of these.
	forbidden := map[string]string{
		"password hash":         user.PasswordHash,
		"TOTP secret":           secret,
		"encrypted TOTP secret": user.TOTPSecret,
	}

	for label, value := range forbidden {
		if value == "" {
			continue
		}
		if bytes.Contains(entries["manifest.json"], []byte(value)) {
			t.Errorf("the manifest contains the %s", label)
		}
		if bytes.Contains(whole, []byte(value)) {
			t.Errorf("the archive contains the %s", label)
		}
	}

	// Nor should it claim to export things it does not.
	if bytes.Contains(entries["manifest.json"], []byte("key_hash")) {
		t.Error("the manifest mentions API key internals")
	}
}

func TestAccountExportOfAnEmptyAccount(t *testing.T) {
	h := newHarness(t)
	me, _ := accountUnderTest(t, h)

	_, entries, _ := readExport(t, me)

	raw, ok := entries["manifest.json"]
	if !ok {
		t.Fatal("an empty account still needs a manifest")
	}

	var manifest exportManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("the manifest is not valid JSON: %v", err)
	}
	if len(manifest.Files) != 0 {
		t.Errorf("manifest lists %d files for an empty account", len(manifest.Files))
	}
	if len(entries) != 1 {
		t.Errorf("archive has %d entries, want just the manifest", len(entries))
	}
}

func TestAccountExportIsScopedToItsOwner(t *testing.T) {
	h := newHarness(t)
	me, _ := accountUnderTest(t, h)

	// The administrator uploads something of their own.
	h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "admins.png", data: pngFixture(t, 24, 24)},
	})
	// And so does the other account.
	me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "mine.png", data: pngFixture(t, 32, 32)},
	})

	_, entries, _ := readExport(t, me)

	var manifest exportManifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}

	if len(manifest.Files) != 1 {
		t.Fatalf("manifest lists %d files, want only the account's own", len(manifest.Files))
	}
	if manifest.Files[0].Name != "mine.png" {
		t.Errorf("manifest includes %q, which belongs to somebody else", manifest.Files[0].Name)
	}
}

func TestAccountExportIncludesAlbums(t *testing.T) {
	h := newHarness(t)
	me, _ := accountUnderTest(t, h)

	resp, body := me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "one.png", data: pngFixture(t, 40, 40)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d", resp.StatusCode)
	}
	id := firstFileID(t, body)

	me.post("/albums", url.Values{"title": {"Summer"}, "description": {"Warm ones"}, "public": {"1"}})
	me.post("/a/summer/files", url.Values{"files": {id}})

	_, entries, _ := readExport(t, me)

	var manifest exportManifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}

	if len(manifest.Albums) != 1 {
		t.Fatalf("manifest lists %d albums, want 1", len(manifest.Albums))
	}
	album := manifest.Albums[0]
	if album.Title != "Summer" || album.Description != "Warm ones" {
		t.Errorf("album = %+v", album)
	}
	if len(album.Files) != 1 || album.Files[0] != id {
		t.Errorf("album membership = %v, want [%s]", album.Files, id)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}
