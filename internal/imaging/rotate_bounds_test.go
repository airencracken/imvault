// SPDX-License-Identifier: AGPL-3.0-or-later
package imaging

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"strings"
	"testing"
)

func TestRotationRejectsOversizedHeadersBeforeDecode(t *testing.T) {
	for _, size := range [][2]uint32{{6000, 4001}, {24000001, 1}, {1, 24000001}} {
		var header bytes.Buffer
		header.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
		_ = binary.Write(&header, binary.BigEndian, uint32(13))
		chunk := []byte("IHDR")
		chunk = binary.BigEndian.AppendUint32(chunk, size[0])
		chunk = binary.BigEndian.AppendUint32(chunk, size[1])
		chunk = append(chunk, 8, 2, 0, 0, 0)
		header.Write(chunk)
		_ = binary.Write(&header, binary.BigEndian, crc32.ChecksumIEEE(chunk))
		_, err := Rotate(bytes.NewReader(header.Bytes()), 90, 95, true)
		if err == nil || !strings.Contains(err.Error(), "too large") {
			t.Fatal("pixel budget not enforced before decoding", size, err)
		}
	}
}

func TestRotationAppliesCameraOrientationBeforeManualCorrection(t *testing.T) {
	original := encodeJPEG(t, 30, 20)
	// A minimal TIFF directory carries EXIF orientation 6 (clockwise 90).
	exif := append([]byte("Exif\x00\x00II"), 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 0x01, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0, 0, 0, 0, 0)
	oriented := append([]byte{0xff, 0xd8, 0xff, 0xe1}, byte((len(exif)+2)>>8), byte(len(exif)+2))
	oriented = append(oriented, exif...)
	oriented = append(oriented, original[2:]...)
	base, err := Rotate(bytes.NewReader(oriented), 0, 95, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(base.Data))
	if err != nil || cfg.Width != 20 || cfg.Height != 30 {
		t.Fatal("camera orientation ignored", cfg, err)
	}
	correction, err := Rotate(bytes.NewReader(oriented), 270, 95, true)
	if err != nil {
		t.Fatal(err)
	}
	corrected, _, err := image.Decode(bytes.NewReader(correction.Data))
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := image.Decode(bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	if corrected.Bounds() != source.Bounds() {
		t.Fatal("manual correction composed in the wrong order")
	}
	for y := 0; y < 20; y++ {
		for x := 0; x < 30; x++ {
			if color.NRGBAModel.Convert(corrected.At(x, y)) != color.NRGBAModel.Convert(source.At(x, y)) {
				t.Fatal("camera orientation and correction did not cancel")
			}
		}
	}
}
