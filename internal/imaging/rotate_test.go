// SPDX-License-Identifier: AGPL-3.0-or-later
package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestRotationPreservesPixelsAlphaAndQuarterTurnGeometry(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			source.SetNRGBA(x, y, color.NRGBA{uint8(40 * x), uint8(50 * y), uint8(20 * (x + y)), uint8(70 + 20*x)})
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, source); err != nil {
		t.Fatal(err)
	}
	for _, degrees := range []int{0, 90, 180, 270} {
		result, err := Rotate(bytes.NewReader(input.Bytes()), degrees, 95, true)
		if err != nil {
			t.Fatal(err)
		}
		decoded, format, err := image.Decode(bytes.NewReader(result.Data))
		if err != nil || format != "png" || result.Mime != "image/png" {
			t.Fatal("lossless format", err)
		}
		w, h := 3, 2
		if degrees == 90 || degrees == 270 {
			w, h = h, w
		}
		if decoded.Bounds().Dx() != w || decoded.Bounds().Dy() != h {
			t.Fatalf("%d: wrong geometry %v", degrees, decoded.Bounds())
		}
		for y := 0; y < 2; y++ {
			for x := 0; x < 3; x++ {
				dx, dy := x, y
				switch degrees {
				case 90:
					dx, dy = 1-y, x
				case 180:
					dx, dy = 2-x, 1-y
				case 270:
					dx, dy = y, 2-x
				}
				if color.NRGBAModel.Convert(decoded.At(dx, dy)) != source.NRGBAAt(x, y) {
					t.Fatalf("%d: changed pixel %d,%d", degrees, x, y)
				}
			}
		}
	}
	// Four turns return every pixel, including alpha, to its original position.
	data := input.Bytes()
	for i := 0; i < 4; i++ {
		result, err := Rotate(bytes.NewReader(data), 90, 95, true)
		if err != nil {
			t.Fatal(err)
		}
		data = result.Data
	}
	restored, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			if color.NRGBAModel.Convert(restored.At(x, y)) != source.NRGBAAt(x, y) {
				t.Fatal("four-turn invariant failed")
			}
		}
	}
	for _, degrees := range []int{-90, 1, 45, 360, 999} {
		if _, err := Rotate(bytes.NewReader(input.Bytes()), degrees, 95, true); err == nil {
			t.Fatal("accepted invalid angle", degrees)
		}
	}
	if _, err := Rotate(bytes.NewReader([]byte("broken image")), 90, 95, true); err == nil {
		t.Fatal("accepted corrupt input")
	}
	result, err := Rotate(bytes.NewReader(input.Bytes()), 90, 95, false)
	if err != nil || result.Mime != "image/png" {
		t.Fatal("preview lost transparency", err)
	}
}
