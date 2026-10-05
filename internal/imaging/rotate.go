// SPDX-License-Identifier: AGPL-3.0-or-later

package imaging

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"io"

	"github.com/disintegration/imaging"
)

// Rotate applies a clockwise quarter turn after honoring camera orientation.
// It always starts from the source, so repeated corrections do not accumulate
// encoding loss. Lossless downloads use PNG, including for opaque photos.
func Rotate(r io.Reader, degrees, quality int, lossless bool) (Rendition, error) {
	if degrees != 0 && degrees != 90 && degrees != 180 && degrees != 270 {
		return Rendition{}, fmt.Errorf("invalid rotation")
	}
	decoded, err := decodeRotationSource(r)
	if err != nil {
		return Rendition{}, err
	}
	var rotated image.Image = decoded
	switch degrees {
	case 90:
		rotated = imaging.Rotate270(decoded)
	case 180:
		rotated = imaging.Rotate180(decoded)
	case 270:
		rotated = imaging.Rotate90(decoded)
	}
	return encodeRotation(rotated, quality, lossless)
}

func encodeRotation(rotated image.Image, quality int, lossless bool) (Rendition, error) {
	var output bytes.Buffer
	if lossless || hasTransparency(rotated) {
		if err := imaging.Encode(&output, rotated, imaging.PNG); err != nil {
			return Rendition{}, err
		}
		return Rendition{Data: output.Bytes(), Ext: "png", Mime: "image/png"}, nil
	}
	if quality < 1 || quality > 100 {
		quality = 95
	}
	if err := imaging.Encode(&output, rotated, imaging.JPEG, imaging.JPEGQuality(quality)); err != nil {
		return Rendition{}, err
	}
	return Rendition{Data: output.Bytes(), Ext: "jpg", Mime: "image/jpeg"}, nil
}

// decodeRotationSource checks the header before allocating pixels, including
// when the source is an existing rendition rather than an upload.
func decodeRotationSource(r io.Reader) (image.Image, error) {
	source, err := asReadSeeker(r)
	if err != nil {
		return nil, err
	}
	if err := rewind(source); err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(bufio.NewReaderSize(source, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read image dimensions: %w", err)
	}
	if err := CheckDimensions(config.Width, config.Height); err != nil {
		return nil, err
	}
	if err := rewind(source); err != nil {
		return nil, err
	}
	decoded, err := imaging.Decode(source, imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return decoded, nil
}
