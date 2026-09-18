// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"imvault/internal/models"
)

// uploadFile is one file in a multipart upload.
type uploadFile struct {
	name string
	data []byte
}

// uploadFiles posts a multipart body through the browser upload endpoint.
func (h *harness) uploadFiles(fields map[string]string, files []uploadFile) (*http.Response, string) {
	h.t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	fields["csrf_token"] = h.csrf()
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			h.t.Fatal(err)
		}
	}
	for _, file := range files {
		part, err := writer.CreateFormFile("files", file.name)
		if err != nil {
			h.t.Fatal(err)
		}
		if _, err := part.Write(file.data); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		h.t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/upload", &body)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
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

// registerForm creates an account through the real form and returns its id.
func (h *harness) registerForm(username string) *models.User {
	h.t.Helper()

	h.get("/")
	resp, _ := h.postForm("/register", map[string][]string{
		"csrf_token": {h.csrf()},
		"username":   {username},
		"password":   {testPassword},
	})
	if resp.StatusCode != http.StatusSeeOther {
		h.t.Fatalf("register = %d, want 303", resp.StatusCode)
	}

	user, err := h.store.UserByUsername(h.t.Context(), username)
	if err != nil {
		h.t.Fatalf("load registered user: %v", err)
	}
	return user
}

const testPassword = "hunter2hunter2"

// firstFileID extracts the id from the first card in an upload response.
func firstFileID(t *testing.T, body string) string {
	t.Helper()

	matches := fileIDRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatalf("no file card in response: %s", truncate(body))
	}
	return matches[0][1]
}

func TestAnimatedGIFUploadKeepsAnimation(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	const frames = 6
	original := animatedGIF(t, frames, 48, 36)

	resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "wiggle.gif", data: original},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d, want 200 (body: %s)", resp.StatusCode, truncate(body))
	}
	id := firstFileID(t, body)

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatalf("load file: %v", err)
	}
	if file.Kind != models.KindAnimated {
		t.Errorf("kind = %q, want animated", file.Kind)
	}
	if file.FrameCount != frames {
		t.Errorf("frame count = %d, want %d", file.FrameCount, frames)
	}
	if file.Mime != "image/gif" {
		t.Errorf("mime = %q, want image/gif", file.Mime)
	}
	if file.Width != 48 || file.Height != 36 {
		t.Errorf("dimensions = %dx%d, want 48x36", file.Width, file.Height)
	}
	// No static preview rendition should have been stored.
	if file.PreviewKey != "" {
		t.Error("an animation should not have a separate preview object")
	}

	// The grid thumbnail is a still image.
	thumbResp, thumb := h.get("/f/" + id + "/thumb")
	if thumbResp.StatusCode != http.StatusOK {
		t.Fatalf("thumb = %d, want 200", thumbResp.StatusCode)
	}
	if ct := thumbResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Errorf("thumb content type = %q, want an image type", ct)
	}
	if _, _, err := image.Decode(strings.NewReader(thumb)); err != nil {
		t.Errorf("thumb is not a decodable still: %v", err)
	}

	// The preview serves the original animation, byte for byte.
	previewResp, preview := h.get("/f/" + id + "/preview")
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview = %d, want 200", previewResp.StatusCode)
	}
	if ct := previewResp.Header.Get("Content-Type"); ct != "image/gif" {
		t.Errorf("preview content type = %q, want image/gif", ct)
	}
	if preview != string(original) {
		t.Errorf("preview is %d bytes, want the %d-byte original", len(preview), len(original))
	}

	// And the detail page calls it out.
	pageResp, page := h.get("/f/" + id)
	if pageResp.StatusCode != http.StatusOK {
		t.Fatalf("file page = %d, want 200", pageResp.StatusCode)
	}
	if !strings.Contains(page, "Animation") {
		t.Error("the detail page does not identify the file as an animation")
	}
}

func TestSingleFrameGIFIsTreatedAsAnImage(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "still.gif", data: animatedGIF(t, 1, 32, 32)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d, want 200", resp.StatusCode)
	}
	id := firstFileID(t, body)

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != models.KindImage {
		t.Errorf("kind = %q, want image for a one-frame gif", file.Kind)
	}
	if file.PreviewKey == "" {
		t.Error("a still gif should get a preview rendition")
	}
}

func TestVideoClipUpload(t *testing.T) {
	h := newHarness(t)
	if !h.processor.VideoEnabled() {
		t.Skip("ffmpeg is not installed")
	}
	h.registerForm("marcus")

	clip := makeWebM(t)

	resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "clip.webm", data: clip},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d, want 200 (body: %s)", resp.StatusCode, truncate(body))
	}
	id := firstFileID(t, body)

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatalf("load file: %v", err)
	}
	if file.Kind != models.KindVideo {
		t.Fatalf("kind = %q, want video", file.Kind)
	}
	if file.Mime != "video/webm" || file.Ext != "webm" {
		t.Errorf("mime/ext = %q/%q, want video/webm and webm", file.Mime, file.Ext)
	}
	if file.DurationMS <= 0 {
		t.Error("expected a probed duration")
	}
	if file.Width != 64 || file.Height != 48 {
		t.Errorf("dimensions = %dx%d, want 64x48", file.Width, file.Height)
	}
	if file.PreviewKey != "" {
		t.Error("a clip should not have a separate preview object")
	}
	if file.ThumbKey == "" {
		t.Fatal("a clip should have a poster thumbnail")
	}

	// The poster is a decodable still.
	thumbResp, thumb := h.get("/f/" + id + "/thumb")
	if thumbResp.StatusCode != http.StatusOK {
		t.Fatalf("thumb = %d, want 200", thumbResp.StatusCode)
	}
	if _, _, err := image.Decode(strings.NewReader(thumb)); err != nil {
		t.Errorf("poster is not decodable: %v", err)
	}

	// The original is served untouched, as video.
	rawResp, _ := h.get("/f/" + id + "/raw")
	if rawResp.StatusCode != http.StatusOK {
		t.Fatalf("raw = %d, want 200", rawResp.StatusCode)
	}
	if ct := rawResp.Header.Get("Content-Type"); ct != "video/webm" {
		t.Errorf("raw content type = %q, want video/webm", ct)
	}
	if got := rawResp.Header.Get("Content-Length"); got != "" {
		// Content-Length is set; make sure it matches what we uploaded.
		if got != strconv.Itoa(len(clip)) {
			t.Errorf("content length = %s, want %d", got, len(clip))
		}
	}

	// Range requests must work, or browsers cannot scrub the clip.
	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/f/"+id+"/raw", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-9")
	rangeResp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer rangeResp.Body.Close()

	if rangeResp.StatusCode != http.StatusPartialContent {
		t.Errorf("range status = %d, want 206", rangeResp.StatusCode)
	}
	if cr := rangeResp.Header.Get("Content-Range"); !strings.HasPrefix(cr, "bytes 0-9/") {
		t.Errorf("content range = %q, want bytes 0-9/...", cr)
	}
	partial, _ := io.ReadAll(rangeResp.Body)
	if len(partial) != 10 {
		t.Errorf("range returned %d bytes, want 10", len(partial))
	}

	// The detail page embeds a player.
	pageResp, page := h.get("/f/" + id)
	if pageResp.StatusCode != http.StatusOK {
		t.Fatalf("file page = %d, want 200", pageResp.StatusCode)
	}
	if !strings.Contains(page, "<video") {
		t.Error("the detail page does not embed a video player")
	}
	if !strings.Contains(page, "Video clip") {
		t.Error("the detail page does not identify the file as a clip")
	}
}

// makeWebM renders a tiny WebM clip with the system ffmpeg.
func makeWebM(t *testing.T) []byte {
	t.Helper()

	path := filepath.Join(t.TempDir(), "clip.webm")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=64x48:rate=5",
		"-t", "1",
		"-c:v", "libvpx", "-pix_fmt", "yuv420p",
		"-y", path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot generate a test clip (%v): %s", err, out)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read clip: %v", err)
	}
	return data
}

// animatedGIF builds an N-frame GIF.
func animatedGIF(t *testing.T, frames, w, h int) []byte {
	t.Helper()

	palette := color.Palette{
		color.RGBA{10, 12, 18, 255},
		color.RGBA{240, 240, 245, 255},
		color.RGBA{110, 168, 254, 255},
		color.RGBA{242, 112, 122, 255},
	}

	animation := &gif.GIF{}
	for i := 0; i < frames; i++ {
		frame := image.NewPaletted(image.Rect(0, 0, w, h), palette)
		for j := range frame.Pix {
			frame.Pix[j] = uint8((j + i) % len(palette))
		}
		animation.Image = append(animation.Image, frame)
		animation.Delay = append(animation.Delay, 5)
	}

	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, animation); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}
