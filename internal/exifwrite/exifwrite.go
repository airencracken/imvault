// SPDX-License-Identifier: AGPL-3.0-or-later

// Package exifwrite builds Exif blocks for fixtures.
//
// No tool in the build environment writes Exif — neither ImageMagick nor ffmpeg
// will — and a hand-built block is the only way to test that reading one works.
// A stub would agree with the reader by construction and prove nothing. So the
// sample media generator uses this to produce a photograph that carries real
// metadata, and the tests that read metadata back use it too, which is why it
// is a package rather than something inside a _test file.
package exifwrite

import (
	"encoding/binary"
	"math"
)

// The TIFF field types.
const (
	typeByte     = 1
	typeASCII    = 2
	typeShort    = 3
	typeLong     = 4
	typeRational = 5
)

// Tags is what to write. Every field is optional; an empty one is left out, and
// that is what makes this useful for testing the absence of metadata as well as
// its presence.
type Tags struct {
	Make      string
	Model     string
	Software  string
	Artist    string
	Copyright string
	LensModel string

	// Taken is written as cameras write it: "2006:01:02 15:04:05".
	Taken string
	// Exposure, Aperture, and FocalLength are numerator and denominator.
	Exposure    [2]uint32
	Aperture    [2]uint32
	FocalLength [2]uint32
	ISO         uint16

	// A location, written only when it is not null island.
	Latitude  float64
	Longitude float64
	Altitude  float64
}

type entry struct {
	tag       uint16
	fieldType uint16
	count     uint32
	inline    uint32
	data      []byte
}

type dir struct {
	entries []entry
	offset  uint32
}

func ascii(tag uint16, value string) entry {
	data := append([]byte(value), 0)
	if len(data) <= 4 {
		var inline [4]byte
		copy(inline[:], data)
		return entry{tag: tag, fieldType: typeASCII, count: uint32(len(data)),
			inline: binary.LittleEndian.Uint32(inline[:])}
	}
	return entry{tag: tag, fieldType: typeASCII, count: uint32(len(data)), data: data}
}

func short(tag, value uint16) entry {
	return entry{tag: tag, fieldType: typeShort, count: 1, inline: uint32(value)}
}

func byteValue(tag uint16, value byte) entry {
	return entry{tag: tag, fieldType: typeByte, count: 1, inline: uint32(value)}
}

func rational(tag uint16, pairs ...[2]uint32) entry {
	data := make([]byte, 0, len(pairs)*8)
	for _, pair := range pairs {
		var raw [8]byte
		binary.LittleEndian.PutUint32(raw[0:4], pair[0])
		binary.LittleEndian.PutUint32(raw[4:8], pair[1])
		data = append(data, raw[:]...)
	}
	return entry{tag: tag, fieldType: typeRational, count: uint32(len(pairs)), data: data}
}

// Block returns an APP1 payload: the Exif identifier followed by a TIFF block,
// which is exactly what a camera writes.
func Block(tags Tags) []byte {
	ifd0 := &dir{}
	if tags.Make != "" {
		ifd0.entries = append(ifd0.entries, ascii(0x010F, tags.Make))
	}
	if tags.Model != "" {
		ifd0.entries = append(ifd0.entries, ascii(0x0110, tags.Model))
	}
	if tags.Software != "" {
		ifd0.entries = append(ifd0.entries, ascii(0x0131, tags.Software))
	}
	if tags.Artist != "" {
		ifd0.entries = append(ifd0.entries, ascii(0x013B, tags.Artist))
	}
	if tags.Copyright != "" {
		ifd0.entries = append(ifd0.entries, ascii(0x8298, tags.Copyright))
	}
	// The pointers to the other two directories; the offsets are patched in.
	ifd0.entries = append(ifd0.entries, entry{tag: 0x8769, fieldType: typeLong, count: 1})
	ifd0.entries = append(ifd0.entries, entry{tag: 0x8825, fieldType: typeLong, count: 1})

	exifIFD := &dir{}
	if tags.Exposure != [2]uint32{} {
		exifIFD.entries = append(exifIFD.entries, rational(0x829A, tags.Exposure))
	}
	if tags.Aperture != [2]uint32{} {
		exifIFD.entries = append(exifIFD.entries, rational(0x829D, tags.Aperture))
	}
	if tags.ISO > 0 {
		exifIFD.entries = append(exifIFD.entries, short(0x8827, tags.ISO))
	}
	if tags.Taken != "" {
		exifIFD.entries = append(exifIFD.entries, ascii(0x9003, tags.Taken))
	}
	if tags.FocalLength != [2]uint32{} {
		exifIFD.entries = append(exifIFD.entries, rational(0x920A, tags.FocalLength))
	}
	if tags.LensModel != "" {
		exifIFD.entries = append(exifIFD.entries, ascii(0xA434, tags.LensModel))
	}

	gps := &dir{}
	if tags.Latitude != 0 || tags.Longitude != 0 {
		latRef, lonRef := "N", "E"
		if tags.Latitude < 0 {
			latRef = "S"
		}
		if tags.Longitude < 0 {
			lonRef = "W"
		}

		gps.entries = append(gps.entries, ascii(0x0001, latRef))
		gps.entries = append(gps.entries, rational(0x0002, sexagesimal(tags.Latitude)...))
		gps.entries = append(gps.entries, ascii(0x0003, lonRef))
		gps.entries = append(gps.entries, rational(0x0004, sexagesimal(tags.Longitude)...))
		gps.entries = append(gps.entries, byteValue(0x0005, 0))
		if tags.Altitude != 0 {
			gps.entries = append(gps.entries, rational(0x0006, [2]uint32{
				uint32(math.Round(math.Abs(tags.Altitude) * 10)), 10,
			}))
		}
	}

	if len(ifd0.entries) == 0 && len(exifIFD.entries) == 0 && len(gps.entries) == 0 {
		return nil
	}
	return append([]byte("Exif\x00\x00"), layout(ifd0, exifIFD, gps)...)
}

// Attach inserts an Exif block into a JPEG immediately after the start of image,
// which is where a camera puts it.
func Attach(jpegBytes []byte, tags Tags) []byte {
	block := Block(tags)
	if block == nil || len(jpegBytes) < 2 || jpegBytes[0] != 0xFF || jpegBytes[1] != 0xD8 {
		return jpegBytes
	}

	length := len(block) + 2
	out := make([]byte, 0, len(jpegBytes)+len(block)+4)
	out = append(out, jpegBytes[:2]...)
	out = append(out, 0xFF, 0xE1, byte(length>>8), byte(length&0xFF))
	out = append(out, block...)
	out = append(out, jpegBytes[2:]...)
	return out
}

// sexagesimal converts decimal degrees into the degrees, minutes, and seconds a
// camera writes, with the seconds carried to two decimal places.
func sexagesimal(degrees float64) [][2]uint32 {
	value := math.Abs(degrees)
	d := math.Floor(value)
	m := math.Floor((value - d) * 60)
	s := (value - d - m/60) * 3600 * 100

	return [][2]uint32{
		{uint32(d), 1},
		{uint32(m), 1},
		{uint32(math.Round(s)), 100},
	}
}

// layout writes the directories and then a data area, resolving the offsets in
// a second pass because the sizes are known before anything is written.
func layout(dirs ...*dir) []byte {
	// The header is eight bytes; each directory follows the last.
	next := uint32(8)
	for _, d := range dirs {
		d.offset = next
		next += dirSize(d)
	}

	// The entries that point at another directory can now be filled in. An
	// empty directory is still given an offset, which is harmless.
	for i := range dirs[0].entries {
		switch dirs[0].entries[i].tag {
		case 0x8769:
			dirs[0].entries[i].inline = dirs[1].offset
		case 0x8825:
			dirs[0].entries[i].inline = dirs[2].offset
		}
	}

	out := []byte{'I', 'I', 0x2A, 0x00}
	out = binary.LittleEndian.AppendUint32(out, dirs[0].offset)
	for _, d := range dirs {
		out = appendDir(out, d)
	}

	// The data area, with each blob's offset recorded as it is appended.
	offsets := map[*entry]uint32{}
	for _, d := range dirs {
		for i := range d.entries {
			e := &d.entries[i]
			if e.data == nil {
				continue
			}
			offsets[e] = uint32(len(out))
			out = append(out, e.data...)
			if len(e.data)%2 != 0 {
				out = append(out, 0)
			}
		}
	}

	// Second pass: the offsets are known now, so the placeholder zeros can be
	// replaced. Walking from the first directory is the part that matters; an
	// earlier version started at the data area and silently wrote nothing.
	cursor := dirs[0].offset
	for _, d := range dirs {
		cursor += 2
		for i := range d.entries {
			e := &d.entries[i]
			if e.data != nil {
				binary.LittleEndian.PutUint32(out[cursor+8:cursor+12], offsets[e])
			}
			cursor += 12
		}
		cursor += 4
	}

	return out
}

func dirSize(d *dir) uint32 { return 2 + 12*uint32(len(d.entries)) + 4 }

func appendDir(out []byte, d *dir) []byte {
	out = binary.LittleEndian.AppendUint16(out, uint16(len(d.entries)))
	for _, e := range d.entries {
		out = binary.LittleEndian.AppendUint16(out, e.tag)
		out = binary.LittleEndian.AppendUint16(out, e.fieldType)
		out = binary.LittleEndian.AppendUint32(out, e.count)
		if e.data != nil {
			out = binary.LittleEndian.AppendUint32(out, 0) // patched above
		} else {
			out = binary.LittleEndian.AppendUint32(out, e.inline)
		}
	}
	return binary.LittleEndian.AppendUint32(out, 0) // no next directory
}
