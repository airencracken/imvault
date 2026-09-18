// SPDX-License-Identifier: AGPL-3.0-or-later

// Package imaging decodes uploaded pictures and derives the thumbnail and
// preview renditions stored alongside them.
package imaging

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"io"

	"github.com/disintegration/imaging"

	// Register additional decoders with the standard image package.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// maxPixels guards against decompression bombs.
const maxPixels = 100 << 20 // 100 megapixels

// Rendition is an encoded derivative image.
type Rendition struct {
	Data []byte
	Ext  string
	Mime string
}

// Result describes a successfully processed upload.
type Result struct {
	Width   int
	Height  int
	Mime    string
	Ext     string
	Thumb   Rendition
	Preview Rendition
}

// Options controls rendition generation.
type Options struct {
	ThumbMax    int
	PreviewMax  int
	JPEGQuality int
}

// maxEncodedBytes bounds how much input Process will buffer when the caller
// cannot supply a seekable reader. Callers are expected to enforce their own
// upload limits; this is a backstop.
const maxEncodedBytes = 128 << 20

// Process decodes an image from r and builds the thumbnail and preview.
//
// Orientation metadata is honoured so that phone photos are not shown
// sideways. Animated inputs are flattened to their first frame.
//
// r is read twice (once to inspect the header, once to decode), so a seekable
// reader is preferred; anything else is buffered in memory.
func Process(r io.Reader, opts Options) (*Result, error) {
	rs, err := asReadSeeker(r)
	if err != nil {
		return nil, err
	}

	// Pass 1: header only, so we can reject bombs before allocating pixels.
	if err := rewind(rs); err != nil {
		return nil, err
	}
	cfg, format, err := image.DecodeConfig(bufio.NewReaderSize(rs, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("unsupported or corrupt image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("image has invalid dimensions %dx%d", cfg.Width, cfg.Height)
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("image is too large to process (%dx%d)", cfg.Width, cfg.Height)
	}

	mime, ext, err := describe(format)
	if err != nil {
		return nil, err
	}

	// Pass 2: full decode from the beginning.
	if err := rewind(rs); err != nil {
		return nil, err
	}
	src, err := imaging.Decode(rs, imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	transparent := hasTransparency(src)
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	thumb, err := render(src, opts.ThumbMax, opts.JPEGQuality, transparent)
	if err != nil {
		return nil, fmt.Errorf("render thumbnail: %w", err)
	}
	preview, err := render(src, opts.PreviewMax, opts.JPEGQuality, transparent)
	if err != nil {
		return nil, fmt.Errorf("render preview: %w", err)
	}

	return &Result{
		Width:   width,
		Height:  height,
		Mime:    mime,
		Ext:     ext,
		Thumb:   thumb,
		Preview: preview,
	}, nil
}

// asReadSeeker returns a rewindable view of r, buffering in memory when r is
// not already seekable.
func asReadSeeker(r io.Reader) (io.ReadSeeker, error) {
	if rs, ok := r.(io.ReadSeeker); ok {
		return rs, nil
	}

	data, err := io.ReadAll(io.LimitReader(r, maxEncodedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	if len(data) > maxEncodedBytes {
		return nil, fmt.Errorf("image exceeds the %d MiB processing limit", maxEncodedBytes>>20)
	}
	return bytes.NewReader(data), nil
}

// rewind returns a seekable reader to its start.
func rewind(rs io.ReadSeeker) error {
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind image: %w", err)
	}
	return nil
}

// Render produces an encoded rendition of img fitted within max on its longest
// edge. Transparency is preserved by encoding to PNG when the image has any
// non-opaque pixel, and to JPEG otherwise.
//
// It is exported so the media pipeline can reuse it for frames extracted from
// animations and clips.
func Render(img image.Image, max, quality int) (Rendition, error) {
	return render(img, max, quality, hasTransparency(img))
}

// render scales img to fit within max on its longest edge. Images already
// smaller than max are kept at their original size.
func render(img image.Image, max, quality int, transparent bool) (Rendition, error) {
	if max <= 0 {
		max = 480
	}
	scaled := imaging.Fit(img, max, max, imaging.Lanczos)

	var buf bytes.Buffer
	if transparent {
		if err := imaging.Encode(&buf, scaled, imaging.PNG); err != nil {
			return Rendition{}, err
		}
		return Rendition{Data: buf.Bytes(), Ext: "png", Mime: "image/png"}, nil
	}

	if err := imaging.Encode(&buf, scaled, imaging.JPEG, imaging.JPEGQuality(quality)); err != nil {
		return Rendition{}, err
	}
	return Rendition{Data: buf.Bytes(), Ext: "jpg", Mime: "image/jpeg"}, nil
}

// hasTransparency reports whether the image contains any pixel that is not
// fully opaque. Sampling keeps this cheap for large photos.
func hasTransparency(img image.Image) bool {
	b := img.Bounds()
	if b.Empty() {
		return false
	}

	// Cheap path: formats that cannot express alpha at all.
	switch img.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16:
		return false
	}

	step := 1
	const samples = 200_000
	if area := b.Dx() * b.Dy(); area > samples {
		step = area/samples + 1
	}

	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if i%step == 0 {
				if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
					return true
				}
			}
			i++
		}
	}
	return false
}

func describe(format string) (mime, ext string, err error) {
	switch format {
	case "jpeg":
		return "image/jpeg", "jpg", nil
	case "png":
		return "image/png", "png", nil
	case "gif":
		return "image/gif", "gif", nil
	case "webp":
		return "image/webp", "webp", nil
	case "bmp":
		return "image/bmp", "bmp", nil
	case "tiff":
		return "image/tiff", "tiff", nil
	default:
		return "", "", fmt.Errorf("unsupported image format %q", format)
	}
}

// IsSupportedFormat reports whether format has a MIME mapping.
func IsSupportedFormat(format string) bool {
	_, _, err := describe(format)
	return err == nil
}
