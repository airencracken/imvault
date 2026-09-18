// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func testProcessor() *Processor {
	return NewProcessor(128, 256, 80, 30*time.Second, nil)
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		head []byte
		want Format
	}{
		{"jpeg", encodeJPEG(t, 8, 8), FormatImage},
		{"png", encodePNG(t, 8, 8), FormatImage},
		{"gif", encodeAnimatedGIF(t, 2, 8, 8), FormatGIF},
		{"webm", []byte{0x1a, 0x45, 0xdf, 0xa3, 0x01, 0x02}, FormatWebM},
		{"mp4", mp4Head("isom"), FormatMP4},
		{"mp4 avc", mp4Head("avc1"), FormatMP4},
		{"mov", mp4Head("qt  "), FormatMOV},
		{"webp still", webpHead(false), FormatWebP},
		{"webp animated", webpHead(true), FormatAnimatedWebP},
		{"empty", nil, FormatImage},
		{"truncated riff", []byte("RIFF"), FormatImage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.head); got != tc.want {
				t.Errorf("Classify(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestFormatMetadata(t *testing.T) {
	if !FormatWebM.IsVideo() || !FormatMP4.IsVideo() || FormatGIF.IsVideo() {
		t.Error("IsVideo is wrong for at least one format")
	}
	if got := FormatWebM.Mime(); got != "video/webm" {
		t.Errorf("webm mime = %q", got)
	}
	if got := FormatMOV.Ext(); got != "mov" {
		t.Errorf("mov ext = %q", got)
	}
	if FormatImage.Mime() != "" || FormatImage.Ext() != "" {
		t.Error("the undetermined format should have no metadata")
	}
}

func TestGIFFrameCount(t *testing.T) {
	for _, frames := range []int{1, 2, 5, 24} {
		data := encodeAnimatedGIF(t, frames, 16, 12)

		got, err := gifFrameCount(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("gifFrameCount(%d frames): %v", frames, err)
		}
		if got != frames {
			t.Errorf("gifFrameCount = %d, want %d", got, frames)
		}
	}
}

func TestGIFFrameCountRejectsNonGIF(t *testing.T) {
	if _, err := gifFrameCount(bytes.NewReader(encodePNG(t, 8, 8))); err == nil {
		t.Error("expected an error for a non-GIF input")
	}
}

func TestGIFFrameCountSurvivesTruncation(t *testing.T) {
	data := encodeAnimatedGIF(t, 6, 16, 16)
	// Chop the tail; a partial animation still has to report as animated.
	truncated := data[:len(data)*3/4]

	frames, err := gifFrameCount(bytes.NewReader(truncated))
	if err != nil {
		t.Fatalf("gifFrameCount(truncated): %v", err)
	}
	if frames < 1 {
		t.Errorf("gifFrameCount(truncated) = %d, want at least 1", frames)
	}
}

func TestProcessStillKeepsAnimatedGIF(t *testing.T) {
	data := encodeAnimatedGIF(t, 8, 40, 30)

	res, err := testProcessor().ProcessStill(bytes.NewReader(data), FormatGIF)
	if err != nil {
		t.Fatalf("ProcessStill: %v", err)
	}

	if res.Kind != "animated" {
		t.Errorf("kind = %q, want animated", res.Kind)
	}
	if res.FrameCount != 8 {
		t.Errorf("frame count = %d, want 8", res.FrameCount)
	}
	if res.Mime != "image/gif" || res.Ext != "gif" {
		t.Errorf("mime/ext = %q/%q, want image/gif and gif", res.Mime, res.Ext)
	}
	if res.Thumb == nil || len(res.Thumb.Data) == 0 {
		t.Fatal("animation is missing a thumbnail")
	}
	// The original animation is what the browser plays, so there must be no
	// separate preview rendition.
	if res.Preview != nil {
		t.Error("animation should not have a static preview rendition")
	}

	// The thumbnail has to be a decodable still.
	if _, _, err := image.Decode(bytes.NewReader(res.Thumb.Data)); err != nil {
		t.Errorf("thumbnail is not decodable: %v", err)
	}
}

func TestProcessStillSingleFrameGIFIsAnImage(t *testing.T) {
	data := encodeAnimatedGIF(t, 1, 40, 30)

	res, err := testProcessor().ProcessStill(bytes.NewReader(data), FormatGIF)
	if err != nil {
		t.Fatalf("ProcessStill: %v", err)
	}
	if res.Kind != "image" {
		t.Errorf("kind = %q, want image for a one-frame gif", res.Kind)
	}
	if res.Preview == nil {
		t.Error("a still image should have a preview rendition")
	}
}

func TestProcessStillPNG(t *testing.T) {
	res, err := testProcessor().ProcessStill(bytes.NewReader(encodePNG(t, 200, 100)), FormatImage)
	if err != nil {
		t.Fatalf("ProcessStill: %v", err)
	}
	if res.Kind != "image" {
		t.Errorf("kind = %q, want image", res.Kind)
	}
	if res.Width != 200 || res.Height != 100 {
		t.Errorf("dimensions = %dx%d, want 200x100", res.Width, res.Height)
	}
	if res.Thumb == nil || res.Preview == nil {
		t.Fatal("expected both renditions")
	}
}

func TestProcessStillRejectsGarbage(t *testing.T) {
	_, err := testProcessor().ProcessStill(bytes.NewReader([]byte("not an image at all")), FormatImage)
	if err == nil {
		t.Fatal("expected an error for non-image input")
	}
}

func TestPlaceholderThumbIsUsable(t *testing.T) {
	thumb := placeholderThumb(480)

	if len(thumb.Data) == 0 {
		t.Fatal("placeholder has no data")
	}
	if thumb.Ext != "jpg" || thumb.Mime != "image/jpeg" {
		t.Errorf("placeholder is %s/%s, want jpg/jpeg", thumb.Ext, thumb.Mime)
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb.Data))
	if err != nil {
		t.Fatalf("placeholder does not decode: %v", err)
	}
	if cfg.Width > 480 || cfg.Height > 480 {
		t.Errorf("placeholder is %dx%d, want to fit within 480", cfg.Width, cfg.Height)
	}
}

func TestProcessVideoWithoutTooling(t *testing.T) {
	// A processor with no video tool must still accept a clip and fall back to
	// a placeholder poster rather than failing.
	proc := NewProcessor(128, 256, 80, 30*time.Second, nil)
	head := []byte{0x1a, 0x45, 0xdf, 0xa3, 0x00}

	res, err := proc.ProcessVideo(context.Background(), bytes.NewReader(head), "", FormatWebM)
	if err != nil {
		t.Fatalf("ProcessVideo: %v", err)
	}
	if res.Kind != "video" {
		t.Errorf("kind = %q, want video", res.Kind)
	}
	if res.Thumb == nil {
		t.Fatal("expected a placeholder thumbnail")
	}
	if len(res.Warnings) == 0 {
		t.Error("expected a warning about the missing tooling")
	}
}

func TestProcessVideoWithFFmpeg(t *testing.T) {
	tool := NewFFmpeg("ffmpeg", "ffprobe")
	if !tool.Available() {
		t.Skip("ffmpeg is not installed")
	}

	path := makeTestClip(t)
	proc := NewProcessor(128, 256, 80, 30*time.Second, tool)

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	res, err := proc.ProcessVideo(context.Background(), file, path, FormatWebM)
	if err != nil {
		t.Fatalf("ProcessVideo: %v", err)
	}

	if res.Kind != "video" {
		t.Errorf("kind = %q, want video", res.Kind)
	}
	if res.Width != 64 || res.Height != 48 {
		t.Errorf("dimensions = %dx%d, want 64x48", res.Width, res.Height)
	}
	if res.DurationMS <= 0 {
		t.Error("expected a probed duration")
	}
	if res.Thumb == nil {
		t.Fatal("expected a poster frame")
	}
	if _, _, err := image.Decode(bytes.NewReader(res.Thumb.Data)); err != nil {
		t.Errorf("poster is not decodable: %v", err)
	}
}

func TestProcessVideoRejectsOverlongClip(t *testing.T) {
	tool := NewFFmpeg("ffmpeg", "ffprobe")
	if !tool.Available() {
		t.Skip("ffmpeg is not installed")
	}

	path := makeTestClip(t)
	// A one-second ceiling against a ~2 second clip.
	proc := NewProcessor(128, 256, 80, time.Second, tool)

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if _, err := proc.ProcessVideo(context.Background(), file, path, FormatWebM); err == nil {
		t.Fatal("expected the duration limit to reject the clip")
	}
}

// makeTestClip renders a tiny WebM using the system ffmpeg.
func makeTestClip(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "clip.webm")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=64x48:rate=5",
		"-t", "2",
		"-c:v", "libvpx", "-pix_fmt", "yuv420p",
		"-y", path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot generate a test clip (%v): %s", err, out)
	}
	return path
}

func encodeAnimatedGIF(t *testing.T, frames, w, h int) []byte {
	t.Helper()

	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
		color.RGBA{220, 60, 60, 255},
		color.RGBA{60, 120, 220, 255},
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

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 180, 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// mp4Head builds a minimal ftyp box with the given major brand.
func mp4Head(brand string) []byte {
	head := make([]byte, 16)
	copy(head[4:8], "ftyp")
	copy(head[8:12], brand)
	return head
}

// webpHead builds a RIFF/WEBP header containing a VP8X chunk whose animation
// flag is set when animated.
func webpHead(animated bool) []byte {
	head := []byte("RIFF")
	head = append(head, 0, 0, 0, 0) // riff size (unused by the sniffer)
	head = append(head, []byte("WEBP")...)
	head = append(head, []byte("VP8X")...)
	head = append(head, 10, 0, 0, 0) // chunk size

	var flags byte
	if animated {
		flags = 0x02
	}
	return append(head, flags, 0, 0, 0)
}
