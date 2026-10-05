// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"imvault/internal/imaging"
)

// screenGIF is an animation whose logical screen is far larger than its
// frames. The decoder sizes nothing from the screen, but every frame may be as
// large as it, so the screen is what the limit has to be checked against.
func screenGIF(t testing.TB, width, height int) []byte {
	t.Helper()
	pal := color.Palette{color.Black, color.White}
	frame := image.NewPaletted(image.Rect(0, 0, 1, 1), pal)
	var buf bytes.Buffer
	animation := &gif.GIF{
		Image:  []*image.Paletted{frame, frame},
		Delay:  []int{1, 1},
		Config: image.Config{ColorModel: pal, Width: width, Height: height},
	}
	if err := gif.EncodeAll(&buf, animation); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAnimatedGIFIsHeldToThePixelLimit(t *testing.T) {
	proc := NewProcessor(64, 128, 80, 0, nil)
	for _, size := range [][2]int{{65535, 65535}, {11000, 11000}, {65535, 1601}} {
		data := screenGIF(t, size[0], size[1])
		if _, err := proc.ProcessStill(bytes.NewReader(data), FormatGIF); err == nil {
			t.Errorf("a %dx%d animation was processed past the %d pixel limit", size[0], size[1], imaging.MaxPixels)
		}
	}
	// Just inside the limit is still an animation.
	res, err := proc.ProcessStill(bytes.NewReader(screenGIF(t, 64, 64)), FormatGIF)
	if err != nil || res.FrameCount != 2 {
		t.Fatalf("a small animation was refused: %v", err)
	}
}

func TestCheckDimensionsDoesNotOverflow(t *testing.T) {
	for _, size := range [][2]int{{0, 1}, {1, 0}, {-1, 5}, {math.MaxInt32, math.MaxInt32}, {math.MaxInt, 2}} {
		if err := imaging.CheckDimensions(size[0], size[1]); err == nil {
			t.Errorf("%dx%d accepted", size[0], size[1])
		}
	}
	if err := imaging.CheckDimensions(6000, 4000); err != nil {
		t.Errorf("exactly the limit refused: %v", err)
	}
}

func FuzzProcessStill(f *testing.F) {
	f.Add(screenGIF(f, 65535, 65535), uint8(0))
	f.Add(encodeAnimatedGIF(f, 3, 8, 8), uint8(0))
	f.Add([]byte("GIF89a\xff\xff\xff\xff\x00\x00\x00,\x00\x00\x00\x00\xff\xff\xff\xff\x00\x02\x02D\x01\x00;"), uint8(0))
	f.Add([]byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x11, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x03, 1, 0x22, 0, 2, 0x11, 1, 3, 0x11, 1}, uint8(1))
	f.Fuzz(func(t *testing.T, data []byte, kind uint8) {
		format := FormatGIF
		if kind%2 == 1 {
			format = Classify(data)
		}
		res, err := NewProcessor(32, 64, 80, 0, nil).ProcessStill(bytes.NewReader(data), format)
		if err != nil {
			return
		}
		if res.Thumb == nil {
			t.Fatal("a processed image has no thumbnail")
		}
		if res.Width > 0 && res.Height > 0 && imaging.CheckDimensions(res.Width, res.Height) != nil {
			t.Fatalf("a %dx%d image passed the pixel limit", res.Width, res.Height)
		}
	})
}

func TestParseProbeFindsADurationWhereverItIsRecorded(t *testing.T) {
	cases := map[string]struct {
		json string
		want int64
	}{
		"container":         {`{"streams":[{"width":4,"height":2}],"format":{"duration":"2.500"}}`, 2500},
		"stream field":      {`{"streams":[{"width":4,"height":2,"duration":"1.25"}],"format":{"duration":"N/A"}}`, 1250},
		"matroska tag":      {`{"streams":[{"width":4,"height":2,"tags":{"DURATION":"00:01:02.500000000"}}],"format":{}}`, 62500},
		"nothing recorded":  {`{"streams":[{"width":4,"height":2}],"format":{}}`, 0},
		"rubbish tag":       {`{"streams":[{"width":4,"height":2,"tags":{"DURATION":"1:99:00"}}],"format":{}}`, 0},
		"negative":          {`{"streams":[{"width":4,"height":2,"duration":"-5"}],"format":{"duration":"-1"}}`, 0},
		"absurdly long":     {`{"streams":[{"width":4,"height":2}],"format":{"duration":"1e300"}}`, math.MaxInt64},
		"not a number tag":  {`{"streams":[{"width":4,"height":2,"tags":{"DURATION":"00:00:NaN"}}],"format":{}}`, 0},
		"container wins":    {`{"streams":[{"width":4,"height":2,"duration":"9"}],"format":{"duration":"3"}}`, 3000},
		"infinite stream":   {`{"streams":[{"width":4,"height":2,"duration":"inf"}],"format":{}}`, math.MaxInt64},
		"hours in the tag":  {`{"streams":[{"width":4,"height":2,"tags":{"DURATION":"01:00:00.0"}}],"format":{}}`, 3_600_000},
		"empty stream list": {`{"streams":[],"format":{"duration":"3"}}`, -1},
	}
	for name, tc := range cases {
		info, err := parseProbe([]byte(tc.json))
		if tc.want < 0 {
			if err == nil {
				t.Errorf("%s: accepted a file with no video stream", name)
			}
			continue
		}
		if err != nil || info.DurationMS != tc.want {
			t.Errorf("%s: duration %d, %v; want %d", name, info.DurationMS, err, tc.want)
		}
	}
}

// probeOnly is a VideoTool that reports a fixed probe result.
type probeOnly struct{ info VideoInfo }

func (p probeOnly) Available() bool { return true }
func (p probeOnly) Probe(context.Context, string) (VideoInfo, error) {
	return p.info, nil
}
func (p probeOnly) Poster(context.Context, string, time.Duration, int) ([]byte, error) {
	return nil, os.ErrNotExist
}
func (p probeOnly) Scrub(context.Context, string, string) error { return nil }

func TestClipWithNoRecordedLengthIsRefusedWhenLengthIsLimited(t *testing.T) {
	tool := probeOnly{VideoInfo{Width: 4, Height: 2}}
	if _, err := NewProcessor(32, 64, 80, time.Minute, tool).ProcessVideo(
		context.Background(), strings.NewReader("clip"), "", FormatMP4); err == nil {
		t.Fatal("a clip of unknown length passed a length limit")
	}
	// With no limit configured, as when rebuilding posters, it is accepted.
	if _, err := NewProcessor(32, 64, 80, 0, tool).ProcessVideo(
		context.Background(), strings.NewReader("clip"), "", FormatMP4); err != nil {
		t.Fatalf("a clip of unknown length was refused with no limit: %v", err)
	}
}

func TestScrubKeepsOnlyPictureAndSound(t *testing.T) {
	tool := NewFFmpeg("ffmpeg", "ffprobe")
	if !tool.Available() {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	subtitles := filepath.Join(dir, "gps.srt")
	if err := os.WriteFile(subtitles, []byte("1\n00:00:00,000 --> 00:00:01,000\nGPS(51.50140,-0.14189) ALT 35m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, dst := filepath.Join(dir, "drone.mp4"), filepath.Join(dir, "clean.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=64x48:rate=5:duration=1",
		"-f", "lavfi", "-i", "sine=duration=1",
		"-i", subtitles,
		"-map", "0", "-map", "1", "-map", "2",
		"-c:v", "mpeg4", "-c:a", "aac", "-c:s", "mov_text", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build a clip with a subtitle track (%v): %s", err, out)
	}
	if err := tool.Scrub(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("51.50140")) {
		t.Fatal("the subtitle track carrying a position survived scrubbing")
	}
	probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", dst).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(probe)); strings.Join(got, ",") != "video,audio" {
		t.Fatalf("scrubbed streams = %v, want video and audio only", got)
	}
}
