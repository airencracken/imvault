// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// This file pins the part of the v1 API that Witmoot depends on, as its
// client in witmoot/internal/imvault/client.go uses it. Witmoot is a separate
// application that talks to a running Imvault over HTTP, so a change here that
// breaks it would otherwise only show up in production. If one of these tests
// fails, the change breaks Witmoot: fix the change, or change Witmoot first and
// then update this contract to match.
//
// What Witmoot relies on:
//
//   - Every request carries "Authorization: Bearer KEY" and
//     "Accept: application/json", and the client never follows redirects.
//   - GET /api/v1/me answers 200 with a JSON object whose "username" is a
//     non-empty string.
//   - GET /api/v1/files?limit=12&offset=N&q=QUERY answers 200 with "files", an
//     array of file objects, and "total", a number. There are more pages while
//     offset + len(files) < total. The body fits in 1 MiB.
//   - GET /api/v1/files/{id} answers 200 with a bare file object (not wrapped)
//     whose "id" is the one asked for.
//   - A file object has string fields "id", "name", "mime", "kind" and
//     "visibility". IDs match ^[a-zA-Z0-9_-]{1,64}$. Witmoot embeds files whose
//     kind is "image" (as the preview rendition) or "animated" (as the thumb).
//   - GET /f/{id}/preview and /f/{id}/thumb with the bearer key answer 200
//     with the bytes of a JPEG, PNG, WebP or GIF image, up to 8 MiB, for the
//     key owner's private files, whatever the Accept header says.
//   - POST /api/v1/upload takes multipart fields "visibility=private" and
//     "metadata=hidden" and one file part named "files", and answers 201 with
//     "files", one file object at visibility "private", and no "errors".
//   - DELETE /api/v1/files/{id} answers 2xx.
//   - Links a person pastes are /f/{id}, /f/{id}/raw, /f/{id}/preview?v=V and
//     /f/{id}/thumb?v=V, where V is 16 lowercase hex digits.
//   - Failures are any other status; Witmoot never parses an error body.

// witmootIDPattern is client.go's IDPattern.
var witmootIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// witmootVersionPattern is client.go's renditionVersionPattern.
var witmootVersionPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

const witmootMaxJSON = 1 << 20
const witmootMaxImage = 8 << 20

// witmootRequest sends a request the way Witmoot's client does.
func (h *harness) witmootRequest(method, path, key, contentType string, body io.Reader) (*http.Response, []byte) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer mustClose(h.t, resp.Body)
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp, data
}

// witmootUpload builds the multipart body Witmoot's Upload sends.
func (h *harness) witmootUpload(key, name string, data []byte) (*http.Response, []byte) {
	h.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range [][2]string{{"visibility", "private"}, {"metadata", "hidden"}} {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			h.t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("files", name)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		h.t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		h.t.Fatal(err)
	}
	return h.witmootRequest(http.MethodPost, "/api/v1/upload", key, writer.FormDataContentType(), &body)
}

// decodeObject requires a JSON object no larger than Witmoot will read.
func decodeObject(t *testing.T, what string, data []byte) map[string]any {
	t.Helper()
	if len(data) > witmootMaxJSON {
		t.Fatalf("%s is %d bytes; Witmoot reads at most %d", what, len(data), witmootMaxJSON)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("%s is not a JSON object: %v: %s", what, err, truncate(string(data)))
	}
	return object
}

// witmootFile checks one file object field by field, with the JSON types
// Witmoot's struct decodes, and returns the fields it reads.
func witmootFile(t *testing.T, what string, value any) map[string]string {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, not a file object", what, value)
	}
	fields := map[string]string{}
	for _, name := range []string{"id", "name", "mime", "kind", "visibility"} {
		raw, present := object[name]
		text, isString := raw.(string)
		if !present || !isString {
			t.Fatalf("%s field %q is %#v, Witmoot needs a string", what, name, raw)
		}
		fields[name] = text
	}
	if !witmootIDPattern.MatchString(fields["id"]) {
		t.Fatalf("%s id %q does not match Witmoot's ID pattern", what, fields["id"])
	}
	return fields
}

// witmootImage fetches a rendition the way Witmoot's Image does.
func (h *harness) witmootImage(key, id, rendition string) {
	h.t.Helper()
	resp, data := h.witmootRequest(http.MethodGet, "/f/"+id+"/"+rendition, key, "", nil)
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("%s of %s = %d, want 200", rendition, id, resp.StatusCode)
	}
	if len(data) > witmootMaxImage {
		h.t.Fatalf("%s of %s is %d bytes; Witmoot reads at most %d", rendition, id, len(data), witmootMaxImage)
	}
	switch mime := http.DetectContentType(data); mime {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
	default:
		h.t.Fatalf("%s of %s sniffs as %q; Witmoot accepts JPEG, PNG, WebP and GIF", rendition, id, mime)
	}
}

// requireLink checks a URL against the shape Witmoot's ParseLink accepts.
func requireLink(t *testing.T, raw, id, rendition string, versioned bool) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := "/f/" + id
	if rendition != "" {
		want += "/" + rendition
	}
	if u.Path != want || u.RawPath != "" || u.Fragment != "" || u.User != nil {
		t.Fatalf("link %q, want path %q", raw, want)
	}
	if !versioned {
		if u.RawQuery != "" {
			t.Fatalf("link %q has a query Witmoot refuses", raw)
		}
		return
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) != 1 || len(query["v"]) != 1 || !witmootVersionPattern.MatchString(query.Get("v")) {
		t.Fatalf("link %q does not carry the single 16-hex-digit v Witmoot accepts", raw)
	}
}

// The whole round trip Witmoot makes: identify the account, upload a private
// image, look it up, list and search, embed its rendition, and delete it.
func TestWitmootContractRoundTrip(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "witmoot", nil)

	resp, data := h.witmootRequest(http.MethodGet, "/api/v1/me", key, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me = %d, want 200", resp.StatusCode)
	}
	if username, ok := decodeObject(t, "me", data)["username"].(string); !ok || username != "alice" {
		t.Fatalf("me username = %#v, want \"alice\"", decodeObject(t, "me", data)["username"])
	}

	resp, data = h.witmootUpload(key, "holiday.png", pngFixture(t, 300, 200))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d, want 201: %s", resp.StatusCode, truncate(string(data)))
	}
	uploaded := decodeObject(t, "upload", data)
	files, ok := uploaded["files"].([]any)
	if !ok || len(files) != 1 {
		t.Fatalf("upload files = %#v, want one file", uploaded["files"])
	}
	if errs, present := uploaded["errors"]; present {
		if list, ok := errs.([]any); errs != nil && (!ok || len(list) != 0) {
			t.Fatalf("upload errors = %#v, Witmoot requires none", errs)
		}
	}
	file := witmootFile(t, "uploaded file", files[0])
	if file["visibility"] != "private" || file["kind"] != "image" || file["name"] != "holiday.png" || file["mime"] != "image/png" {
		t.Fatalf("uploaded file = %v, want a private PNG image named holiday.png", file)
	}
	id := file["id"]
	links := files[0].(map[string]any)
	requireLink(t, links["page_url"].(string), id, "", false)
	requireLink(t, links["raw_url"].(string), id, "raw", false)
	requireLink(t, links["thumb_url"].(string), id, "thumb", true)
	stored, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Metadata) != "hidden" {
		t.Fatalf("metadata=hidden was not applied: %q", stored.Metadata)
	}
	requireLink(t, h.server.URL+stored.PreviewURL(), id, "preview", true)

	resp, data = h.witmootRequest(http.MethodGet, "/api/v1/files/"+id, key, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("file = %d, want 200", resp.StatusCode)
	}
	if fetched := witmootFile(t, "fetched file", decodeObject(t, "file", data)); fetched["id"] != id || fetched["kind"] != "image" {
		t.Fatalf("fetched file = %v, want id %s", fetched, id)
	}

	resp, data = h.witmootRequest(http.MethodGet, "/api/v1/files?limit=12&offset=0&q="+url.QueryEscape("holiday"), key, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d, want 200", resp.StatusCode)
	}
	listed := decodeObject(t, "list", data)
	total, ok := listed["total"].(float64)
	page, isArray := listed["files"].([]any)
	if !ok || !isArray || total != 1 || len(page) != 1 || witmootFile(t, "listed file", page[0])["id"] != id {
		t.Fatalf("list = %s", truncate(string(data)))
	}

	// Witmoot embeds an image's preview; the image is private, so only the
	// bearer key gets it.
	h.witmootImage(key, id, "preview")
	h.witmootImage(key, id, "thumb")
	if resp, _ := h.witmootRequest(http.MethodGet, "/f/"+id+"/preview", "", "", nil); resp.StatusCode == http.StatusOK {
		t.Fatal("a private preview was served without the key")
	}

	resp, _ = h.witmootRequest(http.MethodDelete, "/api/v1/files/"+id, key, "", nil)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("delete = %d, want 2xx", resp.StatusCode)
	}
	if resp, _ := h.witmootRequest(http.MethodGet, "/api/v1/files/"+id, key, "", nil); resp.StatusCode == http.StatusOK {
		t.Fatal("a deleted file is still returned")
	}
}

// Animations are embedded through their thumb, which must be an image too.
func TestWitmootContractAnimatedImages(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "witmoot", nil)
	resp, data := h.witmootUpload(key, "wave.gif", animatedGIF(t, 3, 40, 30))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d: %s", resp.StatusCode, truncate(string(data)))
	}
	files, _ := decodeObject(t, "upload", data)["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("upload = %s", truncate(string(data)))
	}
	file := witmootFile(t, "animation", files[0])
	if file["kind"] != "animated" || file["visibility"] != "private" {
		t.Fatalf("animation = %v, want kind animated at private", file)
	}
	h.witmootImage(key, file["id"], "thumb")
}

// Witmoot asks for twelve at a time and pages until offset + len(files)
// reaches total.
func TestWitmootContractPaging(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "witmoot", nil)
	const count = 15
	images := map[string][]byte{}
	for i := range count {
		images[fmt.Sprintf("cat-%02d.png", i)] = pngFixture(t, 20+i, 20)
	}
	if resp, raw := h.apiUpload(key, nil, images); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed = %d: %s", resp.StatusCode, truncate(string(raw)))
	}

	seen := map[string]bool{}
	for offset := 0; ; {
		resp, data := h.witmootRequest(http.MethodGet, fmt.Sprintf("/api/v1/files?limit=12&offset=%d&q=%s", offset, url.QueryEscape("cat")), key, "", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("page at %d = %d", offset, resp.StatusCode)
		}
		listed := decodeObject(t, "list", data)
		page, _ := listed["files"].([]any)
		total, _ := listed["total"].(float64)
		if total != count || len(page) > 12 || len(page) == 0 {
			t.Fatalf("page at %d has %d files of %v", offset, len(page), total)
		}
		for i, item := range page {
			id := witmootFile(t, fmt.Sprintf("file %d at %d", i, offset), item)["id"]
			if seen[id] {
				t.Fatalf("file %s listed twice", id)
			}
			seen[id] = true
		}
		offset += len(page)
		if offset >= int(total) {
			break
		}
	}
	if len(seen) != count {
		t.Fatalf("paging saw %d files, want %d", len(seen), count)
	}

	// An empty query lists everything; a query matching nothing lists nothing.
	for query, want := range map[string]float64{"": count, "no-such-name": 0} {
		resp, data := h.witmootRequest(http.MethodGet, "/api/v1/files?limit=12&offset=0&q="+url.QueryEscape(query), key, "", nil)
		listed := decodeObject(t, "list", data)
		if resp.StatusCode != http.StatusOK || listed["total"] != want {
			t.Fatalf("q=%q: %d, total %v, want %v", query, resp.StatusCode, listed["total"], want)
		}
		if _, isArray := listed["files"].([]any); !isArray {
			t.Fatalf("q=%q: files is %#v, Witmoot needs an array", query, listed["files"])
		}
	}
}

// Witmoot treats anything but success as unavailable, so every refusal must
// be a non-success status, and none may redirect it elsewhere.
func TestWitmootContractRefusals(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	bob := h.seedUser("bob")
	aliceKey := h.seedKey(alice.ID, "witmoot", nil)
	bobKey := h.seedKey(bob.ID, "witmoot", nil)
	resp, data := h.witmootUpload(aliceKey, "mine.png", pngFixture(t, 30, 30))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d", resp.StatusCode)
	}
	files, _ := decodeObject(t, "upload", data)["files"].([]any)
	id := witmootFile(t, "upload", files[0])["id"]

	for _, tc := range []struct {
		method, path, key string
	}{
		{http.MethodGet, "/api/v1/me", ""},
		{http.MethodGet, "/api/v1/me", "imv_not_a_key"},
		{http.MethodGet, "/api/v1/files?limit=12&offset=0&q=", ""},
		{http.MethodGet, "/api/v1/files/" + id, ""},
		{http.MethodGet, "/api/v1/files/" + id, bobKey},
		{http.MethodGet, "/api/v1/files/no-such-file", aliceKey},
		{http.MethodGet, "/f/" + id + "/preview", bobKey},
		{http.MethodGet, "/f/" + id + "/thumb", ""},
		{http.MethodDelete, "/api/v1/files/" + id, bobKey},
		{http.MethodDelete, "/api/v1/files/" + id, ""},
	} {
		resp, _ := h.witmootRequest(tc.method, tc.path, tc.key, "", nil)
		if resp.StatusCode < 400 {
			t.Errorf("%s %s with key %q = %d, want a refusal", tc.method, tc.path, tc.key, resp.StatusCode)
		}
	}
	// A refused delete left the file in place.
	if resp, _ := h.witmootRequest(http.MethodGet, "/api/v1/files/"+id, aliceKey, "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("the owner lost the file to another key's delete: %d", resp.StatusCode)
	}
	// A non-image upload is refused with no file created for Witmoot to clean up.
	resp, data = h.witmootUpload(aliceKey, "notes.png", []byte(strings.Repeat("not an image ", 10)))
	if resp.StatusCode == http.StatusCreated {
		t.Fatalf("a non-image upload was accepted: %s", truncate(string(data)))
	}
}

// Every route Witmoot calls exists with the method it uses.
func TestWitmootContractRoutesExist(t *testing.T) {
	h := newHarness(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/me"},
		{http.MethodGet, "/api/v1/files"},
		{http.MethodGet, "/api/v1/files/x"},
		{http.MethodPost, "/api/v1/upload"},
		{http.MethodDelete, "/api/v1/files/x"},
		{http.MethodGet, "/f/x/preview"},
		{http.MethodGet, "/f/x/thumb"},
	} {
		resp, _ := h.witmootRequest(route.method, route.path, "", "", nil)
		if resp.StatusCode == http.StatusMethodNotAllowed || (resp.StatusCode == http.StatusNotFound && strings.HasPrefix(route.path, "/api/")) {
			t.Errorf("%s %s = %d; the route is gone", route.method, route.path, resp.StatusCode)
		}
	}
}
