// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"encoding/binary"
	"io"

	"github.com/evanoberholster/imagemeta/meta"
	metajpeg "github.com/evanoberholster/imagemeta/meta/jpeg"
)

type location struct {
	latitude, longitude, altitude *float64
}

// jpegLocation reads GPS independently of imagemeta's forward-only TIFF
// reader, which drops values located before their directory. A JPEG's APP1
// segment is bounded by its 16-bit length, so offsets can be followed in memory
// without buffering the image or trusting a TIFF allocation size.
//
// handled distinguishes a JPEG with no complete GPS fix from another format.
func jpegLocation(r io.ReadSeeker) (gps location, handled bool) {
	defer func() {
		if recover() != nil {
			gps, handled = location{}, true
		}
	}()
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return gps, false
	}
	var signature [2]byte
	if _, err := io.ReadFull(r, signature[:]); err != nil || !looksLikeJPEG(signature[:]) {
		return gps, false
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return gps, false
	}
	err := metajpeg.ScanJPEG(r, func(payload io.Reader, _ meta.ExifHeader) error {
		data, err := io.ReadAll(io.LimitReader(payload, 1<<16))
		if err == nil {
			gps = tiffLocation(data)
		}
		return err
	}, nil)
	if err != nil {
		return location{}, true
	}
	return gps, true
}

type gpsTIFF struct {
	data  []byte
	order binary.ByteOrder
}

func tiffLocation(data []byte) location {
	if len(data) < 8 {
		return location{}
	}
	var order binary.ByteOrder
	switch string(data[:4]) {
	case "II\x2a\x00":
		order = binary.LittleEndian
	case "MM\x00\x2a":
		order = binary.BigEndian
	default:
		return location{}
	}
	tiff := gpsTIFF{data: data, order: order}
	root := tiff.directory(order.Uint32(data[4:8]))
	pointer := tiff.entry(root, 0x8825, 4, 1)
	if pointer == nil {
		return location{}
	}
	return tiff.location(tiff.directory(order.Uint32(pointer)))
}

// directory follows only the root and GPS pointers. It never recurses through
// thumbnails, maker notes, or next-directory links from untrusted files.
func (t gpsTIFF) directory(offset uint32) []byte {
	start := uint64(offset)
	if start < 8 || start+2 > uint64(len(t.data)) {
		return nil
	}
	size := uint64(t.order.Uint16(t.data[start:])) * 12
	start += 2
	if start+size+4 > uint64(len(t.data)) {
		return nil
	}
	return t.data[start : start+size]
}

// entry returns the four-byte value/offset slot only for the expected type and
// count. GPS values have fixed sizes; none require attacker-sized allocations.
func (t gpsTIFF) entry(directory []byte, tag, kind uint16, count uint32) []byte {
	for len(directory) >= 12 {
		entry := directory[:12]
		directory = directory[12:]
		if t.order.Uint16(entry) == tag && t.order.Uint16(entry[2:]) == kind &&
			t.order.Uint32(entry[4:]) == count {
			return entry[8:12]
		}
	}
	return nil
}

func (t gpsTIFF) location(directory []byte) location {
	// V means the receiver did not have a valid fix.
	if status := t.entry(directory, 0x0009, 2, 2); status != nil && status[0] == 'V' {
		return location{}
	}
	lat := t.coordinate(directory, 0x0001, 'N', 'S', 90)
	lon := t.coordinate(directory, 0x0003, 'E', 'W', 180)
	if lat == nil || lon == nil {
		return location{}
	}
	gps := location{latitude: lat, longitude: lon}
	altitude := t.entry(directory, 0x0006, 5, 1)
	ref := t.entry(directory, 0x0005, 1, 1)
	if altitude != nil && ref != nil && ref[0] <= 1 {
		if value, ok := t.rational(uint64(t.order.Uint32(altitude))); ok {
			if ref[0] == 1 {
				value = -value
			}
			gps.altitude = &value
		}
	}
	return gps
}

func (t gpsTIFF) coordinate(directory []byte, refTag uint16, positive, negative byte, limit float64) *float64 {
	ref := t.entry(directory, refTag, 2, 2)
	value := t.entry(directory, refTag+1, 5, 3)
	if ref == nil || value == nil || (ref[0] != positive && ref[0] != negative) {
		return nil
	}
	offset := uint64(t.order.Uint32(value))
	degrees, dOK := t.rational(offset)
	minutes, mOK := t.rational(offset + 8)
	seconds, sOK := t.rational(offset + 16)
	if !dOK || !mOK || !sOK || minutes >= 60 || seconds >= 60 {
		return nil
	}
	coordinate := degrees + minutes/60 + seconds/3600
	if coordinate > limit {
		return nil
	}
	if ref[0] == negative {
		coordinate = -coordinate
	}
	return &coordinate
}

func (t gpsTIFF) rational(offset uint64) (float64, bool) {
	if offset < 8 || offset+8 > uint64(len(t.data)) {
		return 0, false
	}
	value := t.data[offset : offset+8]
	numerator, denominator := t.order.Uint32(value), t.order.Uint32(value[4:])
	if denominator == 0 {
		return 0, false
	}
	return float64(numerator) / float64(denominator), true
}
