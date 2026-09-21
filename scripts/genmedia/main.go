// SPDX-License-Identifier: AGPL-3.0-or-later

// Command genmedia writes the sample images used by scripts/demo.sh.
//
// It is a development helper, not part of the server: generating the fixtures in
// Go keeps the demo self-contained, with no ImageMagick or other tooling needed
// beyond the Go toolchain that is already building the project.
package main

import (
	"bytes"
	"flag"

	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"imvault/internal/exifwrite"
	"math"
	"os"
	"path/filepath"
)

func main() {
	dir := flag.String("dir", ".", "directory to write the sample media into")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fail(err)
	}

	fixtures := []struct {
		name  string
		write func(string) error
	}{
		{"gradient.jpg", writeGradientJPEG},
		{"holiday.jpg", writePhotoWithMetadata},
		{"transparent.png", writeTransparentPNG},
		{"animation.gif", writeAnimationGIF},
	}

	for _, fixture := range fixtures {
		path := filepath.Join(*dir, fixture.name)
		if err := fixture.write(path); err != nil {
			fail(fmt.Errorf("writing %s: %w", fixture.name, err))
		}
		fmt.Printf("  %s\n", path)
	}
}

// writeGradientJPEG produces a large, opaque photo-like image. At 1600x1000 it
// is bigger than the preview bound, so it exercises downscaling.
func writeGradientJPEG(path string) error {
	const (
		width  = 1600
		height = 1000
	)

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		fy := float64(y) / float64(height-1)

		for x := 0; x < width; x++ {
			fx := float64(x) / float64(width-1)
			t := (fx + fy) / 2

			// Deep blue into warm orange, with a soft diagonal ripple so the
			// thumbnails do not look like flat colour.
			ripple := 12 * math.Sin((fx*6+fy*4)*math.Pi)

			img.Set(x, y, color.RGBA{
				R: clamp(28 + t*218 + ripple),
				G: clamp(60 + t*113 + ripple),
				B: clamp(120 - t*35 + ripple),
				A: 255,
			})
		}
	}

	return writeFile(path, func(f *os.File) error {
		return jpeg.Encode(f, img, &jpeg.Options{Quality: 90})
	})
}

// writePhotoWithMetadata produces a photograph that carries the metadata a
// phone would write, including a location.
//
// It exists so the demo has something whose details section is worth opening,
// and so a public copy of it is worth checking: the same file is what shows
// whether coordinates are being kept out of what strangers are served.
func writePhotoWithMetadata(path string) error {
	const (
		width  = 1200
		height = 800
	)

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		fy := float64(y) / float64(height-1)
		for x := 0; x < width; x++ {
			fx := float64(x) / float64(width-1)
			// A warm sky over water, so it reads as a holiday photograph.
			img.Set(x, y, color.RGBA{
				R: clamp(240 - fy*120 + 10*math.Sin(fx*8)),
				G: clamp(170 - fy*60 + 20*math.Sin(fx*5+fy*3)),
				B: clamp(90 + fy*140),
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		return err
	}

	withMetadata := exifwrite.Attach(buf.Bytes(), exifwrite.Tags{
		Make:        "Example Camera Co.",
		Model:       "Example One",
		Software:    "Example Camera Firmware 1.2",
		Artist:      "The Photographer",
		Copyright:   "(c) 2026 The Photographer",
		LensModel:   "Example 35mm f/1.8",
		Taken:       "2026:06:14 19:42:10",
		Exposure:    [2]uint32{1, 500},
		Aperture:    [2]uint32{18, 10},
		FocalLength: [2]uint32{35, 1},
		ISO:         100,
		Latitude:    51.5007,
		Longitude:   -0.1246,
		Altitude:    12,
	})

	return writeFile(path, func(f *os.File) error {
		_, err := f.Write(withMetadata)
		return err
	})
}

// writeTransparentPNG produces an image with genuinely varied alpha, so it
// exercises the transparency detection that decides PNG versus JPEG output.
func writeTransparentPNG(path string) error {
	const size = 800

	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// A soft-edged disc.
	centre := float64(size) / 2
	radius := float64(size) * 0.38
	const feather = 40

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			distance := math.Hypot(float64(x)-centre, float64(y)-centre)
			if distance > radius {
				continue
			}

			alpha := uint8(255)
			if distance > radius-feather {
				alpha = clamp(float64(255) * (radius - distance) / feather)
			}
			img.Set(x, y, color.RGBA{R: 110, G: 168, B: 254, A: alpha})
		}
	}

	// A translucent bar overlapping the disc.
	for y := int(centre) - 40; y < int(centre)+40; y++ {
		for x := 60; x < size-60; x++ {
			if x < 0 || y < 0 || x >= size || y >= size {
				continue
			}
			img.Set(x, y, color.RGBA{R: 246, G: 173, B: 85, A: 140})
		}
	}

	return writeFile(path, func(f *os.File) error {
		return png.Encode(f, img)
	})
}

// writeAnimationGIF produces a multi-frame GIF, so the demo shows off the
// animated handling: a still thumbnail in the grid and the real animation on
// the detail page.
func writeAnimationGIF(path string) error {
	const (
		width  = 360
		height = 240
		frames = 16
	)

	palette := color.Palette{
		color.RGBA{R: 16, G: 18, B: 26, A: 255},
		color.RGBA{R: 110, G: 168, B: 254, A: 255},
		color.RGBA{R: 246, G: 173, B: 85, A: 255},
		color.RGBA{R: 242, G: 112, B: 122, A: 255},
		color.RGBA{R: 110, G: 231, B: 168, A: 255},
	}

	animation := &gif.GIF{LoopCount: 0}
	barWidth := width / 5

	for frame := 0; frame < frames; frame++ {
		img := image.NewPaletted(image.Rect(0, 0, width, height), palette)

		// A bar sweeping left to right, changing colour as it goes.
		progress := float64(frame) / float64(frames)
		left := int(progress*float64(width+barWidth)) - barWidth
		shade := uint8(1 + frame%4)

		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				if x >= left && x < left+barWidth {
					img.SetColorIndex(x, y, shade)
				}
			}
		}

		animation.Image = append(animation.Image, img)
		animation.Delay = append(animation.Delay, 8) // 80ms per frame
	}

	return writeFile(path, func(f *os.File) error {
		return gif.EncodeAll(f, animation)
	})
}

func writeFile(path string, encode func(*os.File) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := encode(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// clamp narrows a float to a valid 8-bit channel value.
func clamp(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return uint8(v)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genmedia:", err)
	os.Exit(1)
}
