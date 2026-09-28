// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"

	"github.com/evanoberholster/imagemeta/meta"
	metajpeg "github.com/evanoberholster/imagemeta/meta/jpeg"
)

// Filter keeps one category of standard EXIF, rebuilding it from bounded,
// known fields. XMP, IPTC, maker notes, thumbnails and unknown metadata are
// removed so they cannot retain a second copy of a hidden GPS directory.
// Compressed image data is unchanged. Unsupported containers fail closed.
func Filter(data []byte, mime string, keepEXIF, keepLocation bool) ([]byte, bool) {
	if keepEXIF && keepLocation {
		return data, true
	}
	clean, ok := Strip(data, mime)
	if !ok || (!keepEXIF && !keepLocation) {
		return clean, ok
	}
	var block []byte
	for _, source := range imageTIFFs(data) {
		block = filteredTIFF(source, keepEXIF)
		if len(block) != 0 {
			break
		}
	}
	if len(block) == 0 {
		return clean, true
	}
	switch {
	case looksLikeJPEG(clean):
		payload := append([]byte("Exif\x00\x00"), block...)
		out := append([]byte{}, clean[:2]...)
		out = append(out, 0xff, jpegAPP1)
		out = binary.BigEndian.AppendUint16(out, uint16(len(payload)+2))
		out = append(out, payload...)
		return append(out, clean[2:]...), true
	case looksLikePNG(clean):
		if len(clean) < 33 || string(clean[12:16]) != "IHDR" {
			return nil, false
		}
		out := append([]byte{}, clean[:33]...)
		out = binary.BigEndian.AppendUint32(out, uint32(len(block)))
		chunk := append([]byte("eXIf"), block...)
		out = append(out, chunk...)
		out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
		return append(out, clean[33:]...), true
	case looksLikeWebP(clean):
		return attachWebPExif(clean, block)
	}
	return clean, true
}

// imageTIFFs only locates container payloads; TIFF offsets are never followed
// until they have been confined to one payload by filteredTIFF.
func imageTIFFs(data []byte) (blocks [][]byte) {
	if looksLikeJPEG(data) {
		// The container library may panic on malformed input; none of it is
		// allowed to turn a filtering attempt into serving the original.
		defer func() { _ = recover() }()
		_ = metajpeg.ScanJPEG(bytes.NewReader(data), func(r io.Reader, _ meta.ExifHeader) error {
			block, err := io.ReadAll(io.LimitReader(r, 1<<16))
			if err == nil {
				blocks = append(blocks, block)
			}
			return err
		}, nil)
		return blocks
	}
	start, trailer, padded := 8, 4, false
	var order binary.ByteOrder = binary.BigEndian
	if looksLikeWebP(data) {
		start, trailer, padded, order = 12, 0, true, binary.LittleEndian
	} else if !looksLikePNG(data) {
		return nil
	}
	for start+8 <= len(data) {
		sizeAt, kindAt := start, start+4
		if padded {
			sizeAt, kindAt = start+4, start
		}
		size := uint64(order.Uint32(data[sizeAt:]))
		end := uint64(start) + 8 + size
		if end+uint64(trailer) > uint64(len(data)) {
			break
		}
		kind := string(data[kindAt : kindAt+4])
		if kind == "eXIf" || kind == "EXIF" {
			blocks = append(blocks, bytes.TrimPrefix(data[start+8:end], []byte("Exif\x00\x00")))
		}
		if kind == "IEND" {
			break
		}
		start = int(end) + trailer
		if padded {
			start += int(size & 1)
		}
	}
	return blocks
}

func attachWebPExif(clean, block []byte) ([]byte, bool) {
	out := append([]byte{}, clean...)
	found := false
	for at := 12; at+8 <= len(out); {
		size := int(binary.LittleEndian.Uint32(out[at+4:]))
		if size < 0 || at+8+size > len(out) {
			return nil, false
		}
		if string(out[at:at+4]) == "VP8X" && size == 10 {
			out[at+8] |= webpFlagExif
			found = true
		}
		at += 8 + size + (size & 1)
	}
	if !found {
		return nil, false
	}
	out = append(out, "EXIF"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(block)))
	out = append(out, block...)
	if len(block)&1 != 0 {
		out = append(out, 0)
	}
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	return out, true
}
