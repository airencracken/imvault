// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

// uploadValues posts the web upload form with repeated fields, the way a
// browser sends a hidden input followed by a checkbox of the same name.
func uploadValues(t *testing.T, s *session, values url.Values) (*http.Response, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, list := range values {
		for _, value := range list {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	part, err := writer.CreateFormFile("files", "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(pngFixture(t, 12, 12)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, s.h.server.URL+"/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-CSRF-Token", s.csrf)
	req.Header.Set("HX-Request", "true")
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer mustClose(t, resp.Body)
	return resp, readAll(t, resp.Body)
}

func TestUploadFormSharingCheckboxes(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	owner := h.sessionFor(t, alice.ID)

	cases := []struct {
		name             string
		values           url.Values
		camera, location bool
	}{
		{"both ticked", url.Values{"visibility": {"public"}, "metadata": {"hidden", "shown"}, "location": {"hidden", "shown"}}, true, true},
		{"both unticked", url.Values{"visibility": {"members"}, "metadata": {"hidden"}, "location": {"hidden"}}, false, false},
		{"camera only", url.Values{"visibility": {"members"}, "metadata": {"hidden", "shown"}, "location": {"hidden"}}, true, false},
		{"public default", url.Values{"visibility": {"public"}}, false, false},
		{"members default", url.Values{"visibility": {"members"}}, true, true},
		{"private default", url.Values{"visibility": {"private"}}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := uploadValues(t, owner, tc.values)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("upload = %d: %s", resp.StatusCode, truncate(body))
			}
			file, err := h.store.FileByID(t.Context(), firstFileID(t, body))
			if err != nil {
				t.Fatal(err)
			}
			if file.ShowCamera != tc.camera || file.ShowLocation != tc.location {
				t.Fatalf("camera=%v location=%v, want %v %v", file.ShowCamera, file.ShowLocation, tc.camera, tc.location)
			}
		})
	}

	for _, bad := range [][]string{{"inherit"}, {"follow"}, {"hidden", "maybe"}, {"<script>alert(1)</script>"}, {strings.Repeat("x", 4096)}} {
		resp, body := uploadValues(t, owner, url.Values{"visibility": {"members"}, "location": bad})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("location %q = %d", bad, resp.StatusCode)
		}
		if strings.Contains(body, "<script>alert") {
			t.Fatal("a rejected value was echoed into the page")
		}
	}
}

func TestAnonymousUploadsShareNeitherChoice(t *testing.T) {
	h := newHarness(t)
	h.setPolicy(t, models.Settings{AllowAnonymousUploads: true, DefaultVisibility: models.VisibilityMembers})
	h.get("/upload")
	resp, body := h.uploadFiles(map[string]string{"metadata": "shown", "location": "shown"}, []uploadFile{{name: "anon.png", data: pngFixture(t, 16, 16)}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous upload = %d: %s", resp.StatusCode, truncate(body))
	}
	file, err := h.store.FileByID(t.Context(), firstFileID(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if file.ShowCamera || file.ShowLocation {
		t.Fatal("an anonymous upload shares its metadata")
	}
}

func TestSharingRoutesToggleRefuseAndProtect(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	owner := h.sessionFor(t, alice.ID)
	id := h.uploadOne(h.seedKey(alice.ID, "test", nil), "photo.png", 20, 20)

	for _, field := range []string{"metadata", "location"} {
		path := "/f/" + id + "/" + field
		for _, value := range []string{"hidden", "shown"} {
			resp, _ := owner.post(path, url.Values{field: {value}})
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("%s=%s: %d", field, value, resp.StatusCode)
			}
			file, _ := h.store.FileByID(t.Context(), id)
			got := file.ShowCamera
			if field == "location" {
				got = file.ShowLocation
			}
			if got != (value == "shown") {
				t.Fatalf("%s=%s stored %v", field, value, got)
			}
		}
		for _, bad := range []url.Values{{}, {field: {""}}, {field: {"inherit"}}, {field: {"shown\x00"}}, {field: {"<b>"}}} {
			resp, body := owner.post(path, bad)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s %v = %d", field, bad, resp.StatusCode)
			}
			// The styled error page, in plain words.
			if !strings.Contains(body, `class="code"`) || !strings.Contains(body, "Choose whether to show or hide this.") {
				t.Fatalf("bad %s response is not the styled page: %s", field, truncate(body))
			}
		}
		resp, _ := h.sessionFor(t, h.seedUser("mallory-"+field).ID).post(path, url.Values{field: {"shown"}})
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("another member changed %s: %d", field, resp.StatusCode)
		}
		resp, _ = h.newSession(t).post(path, url.Values{field: {"shown"}})
		if resp.StatusCode == http.StatusSeeOther && !strings.Contains(resp.Header.Get("Location"), "login") {
			t.Fatalf("a guest changed %s", field)
		}
	}
}

func TestMakingAFilePublicFromThePageHidesBoth(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	owner := h.sessionFor(t, alice.ID)
	id := h.uploadOne(h.seedKey(alice.ID, "test", nil), "photo.png", 20, 20)
	for _, field := range []string{"metadata", "location"} {
		owner.post("/f/"+id+"/"+field, url.Values{field: {"shown"}})
	}
	owner.post("/f/"+id+"/visibility", url.Values{"visibility": {"members"}})
	resp, _ := owner.post("/f/"+id+"/visibility", url.Values{"visibility": {"public"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("visibility = %d", resp.StatusCode)
	}
	file, _ := h.store.FileByID(t.Context(), id)
	if file.ShowCamera || file.ShowLocation {
		t.Fatal("making the file public left its metadata shown")
	}
	_, page := owner.get("/f/" + id)
	for _, want := range []string{"Camera details are <strong>hidden</strong>", "Location is <strong>hidden</strong>", "Show camera details", "Show location"} {
		if !strings.Contains(page, want) {
			t.Errorf("file page is missing %q", want)
		}
	}
}

func TestFilePageShowsContentBeforeEditControls(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	owner := h.sessionFor(t, alice.ID)
	id := h.uploadOne(h.seedKey(alice.ID, "test", nil), "photo.png", 20, 20)
	_, page := owner.get("/f/" + id)
	edit := strings.Index(page, `<details class="panel stack file-edit">`)
	if edit < 0 {
		t.Fatal("the edit controls are not behind one disclosure")
	}
	for _, before := range []string{`<dl class="facts">`, "<h2>Tags</h2>", "<h2>Links</h2>"} {
		if i := strings.Index(page, before); i < 0 || i > edit {
			t.Errorf("%q does not come before the edit controls", before)
		}
	}
	for _, inside := range []string{`action="/f/` + id + `/rename"`, `action="/f/` + id + `/description"`, `action="/f/` + id + `/visibility"`, `action="/f/` + id + `/metadata"`, `action="/f/` + id + `/location"`} {
		if i := strings.Index(page, inside); i < edit {
			t.Errorf("%s is outside the edit disclosure", inside)
		}
	}
	if !strings.Contains(page, "<h2>Move to Trash</h2>") || strings.Contains(page, "Danger zone") {
		t.Error("the delete section is not called Move to Trash")
	}
	if strings.Contains(page, "onclick=") || strings.Contains(page, "Follow EXIF") || strings.Contains(page, "inherit") {
		t.Error("the file page still has inline handlers or the old inherit wording")
	}

	_, albums := owner.get("/albums")
	if strings.Contains(albums, `name="metadata"`) || strings.Contains(albums, `name="location"`) {
		t.Error("the album form still offers metadata settings")
	}
}
