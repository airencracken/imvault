// SPDX-License-Identifier: AGPL-3.0-or-later

package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
)

func testOptions() Options {
	return Options{ThumbMax: 128, PreviewMax: 512, JPEGQuality: 82}
}

func TestProcessJPEG(t *testing.T) {
	data := encodeJPEG(t, 800, 600)

	res, err := Process(bytes.NewReader(data), testOptions())
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if res.Width != 800 || res.Height != 600 {
		t.Errorf("dimensions = %dx%d, want 800x600", res.Width, res.Height)
	}
	if res.Mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", res.Mime)
	}
	if len(res.Thumb.Data) == 0 || len(res.Preview.Data) == 0 {
		t.Fatal("renditions are empty")
	}
	if res.Thumb.Ext != "jpg" {
		t.Errorf("thumb ext = %q, want jpg", res.Thumb.Ext)
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(res.Thumb.Data))
	if err != nil {
		t.Fatalf("decode thumb: %v", err)
	}
	if cfg.Width > 128 || cfg.Height > 128 {
		t.Errorf("thumb is %dx%d, want to fit within 128", cfg.Width, cfg.Height)
	}
}

func TestProcessPNGKeepsTransparency(t *testing.T) {
	data := encodePNGWithAlpha(t, 300, 300)

	res, err := Process(bytes.NewReader(data), testOptions())
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if res.Thumb.Mime != "image/png" {
		t.Errorf("thumb mime = %q, want image/png for transparent input", res.Thumb.Mime)
	}
}

func TestProcessOpaquePNGUsesJPEG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 200, B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	res, err := Process(bytes.NewReader(buf.Bytes()), testOptions())
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if res.Thumb.Ext != "jpg" {
		t.Errorf("thumb ext = %q, want jpg for opaque image", res.Thumb.Ext)
	}
}

func TestProcessRejectsGarbage(t *testing.T) {
	if _, err := Process(bytes.NewReader([]byte("definitely not an image")), testOptions()); err == nil {
		t.Fatal("expected an error for non-image input")
	}
}

// TestProcessFixture runs against a real photo when one is present, which
// exercises the header-rewind path with a larger buffered stream.
func TestProcessFixture(t *testing.T) {
	const fixture = "/tmp/opencode/photo1.jpg"
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}

	res, err := Process(bytes.NewReader(data), testOptions())
	if err != nil {
		t.Fatalf("Process(%s): %v", fixture, err)
	}
	if res.Width != 1600 || res.Height != 1000 {
		t.Errorf("dimensions = %dx%d, want 1600x1000", res.Width, res.Height)
	}
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodePNGWithAlpha(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 220, G: 40, B: 40, A: 0})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
