// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	"imvault/internal/imaging"
)

// placeholderThumb renders a neutral 16:9 poster used when a frame genuinely
// cannot be extracted (no ffmpeg, or an undecodable clip). The grid overlays a
// play badge for video kinds via CSS, so the artwork itself stays plain.
func placeholderThumb(max int) imaging.Rendition {
	if max < 64 {
		max = 64
	}
	width := max
	height := max * 9 / 16
	if height < 32 {
		height = 32
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		t := float64(y) / float64(height-1)

		// A subtle vertical gradient between two muted slate tones.
		r := uint8(26 + t*12)
		g := uint8(30 + t*14)
		b := uint8(38 + t*18)

		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}

	if rendered, err := imaging.Render(img, max, 80); err == nil {
		return rendered
	}

	// Render only fails on encoder trouble; fall back to PNG so the caller
	// always has usable bytes.
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return imaging.Rendition{Ext: "png", Mime: "image/png"}
	}
	return imaging.Rendition{Data: buf.Bytes(), Ext: "png", Mime: "image/png"}
}
