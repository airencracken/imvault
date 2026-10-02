// SPDX-License-Identifier: AGPL-3.0-or-later

package exifwrite

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"math"
	"testing"
)

// entry reads one directory entry back, following the layout Block writes.
type readEntry struct {
	tag, kind uint16
	count     uint32
	value     []byte
}

func directory(t *testing.T, tiff []byte, offset uint32) map[uint16]readEntry {
	t.Helper()
	if int(offset)+2 > len(tiff) {
		t.Fatalf("directory offset %d is outside the block", offset)
	}
	n := int(binary.LittleEndian.Uint16(tiff[offset:]))
	out := map[uint16]readEntry{}
	sizes := map[uint16]int{typeByte: 1, typeASCII: 1, typeShort: 2, typeLong: 4, typeRational: 8}
	for i := 0; i < n; i++ {
		at := int(offset) + 2 + 12*i
		e := readEntry{
			tag:   binary.LittleEndian.Uint16(tiff[at:]),
			kind:  binary.LittleEndian.Uint16(tiff[at+2:]),
			count: binary.LittleEndian.Uint32(tiff[at+4:]),
		}
		size := sizes[e.kind] * int(e.count)
		if size <= 4 {
			e.value = tiff[at+8 : at+8+size]
		} else {
			start := int(binary.LittleEndian.Uint32(tiff[at+8:]))
			if start+size > len(tiff) {
				t.Fatalf("tag %#x points outside the block", e.tag)
			}
			e.value = tiff[start : start+size]
		}
		out[e.tag] = e
	}
	return out
}

func readRational(e readEntry, i int) float64 {
	n := binary.LittleEndian.Uint32(e.value[8*i:])
	d := binary.LittleEndian.Uint32(e.value[8*i+4:])
	return float64(n) / float64(d)
}

func TestBlockWritesWhatItWasGiven(t *testing.T) {
	block := Block(Tags{
		Make: "TestCam", Model: "One", Artist: "A Photographer", Taken: "2026:01:02 15:04:05",
		Exposure: [2]uint32{1, 250}, ISO: 200, Latitude: -33.5, Longitude: 151.25, Altitude: -15,
	})
	if !bytes.HasPrefix(block, []byte("Exif\x00\x00II\x2a\x00")) {
		t.Fatalf("not an Exif block: %q", block[:12])
	}
	tiff := block[6:]
	root := directory(t, tiff, binary.LittleEndian.Uint32(tiff[4:]))
	if got := string(bytes.TrimRight(root[0x010F].value, "\x00")); got != "TestCam" {
		t.Errorf("make = %q", got)
	}
	exif := directory(t, tiff, binary.LittleEndian.Uint32(root[0x8769].value))
	if readRational(exif[0x829A], 0) != 1.0/250 || binary.LittleEndian.Uint16(exif[0x8827].value) != 200 {
		t.Error("exposure or ISO misread")
	}
	gps := directory(t, tiff, binary.LittleEndian.Uint32(root[0x8825].value))
	if string(gps[0x0001].value[:1]) != "S" || string(gps[0x0003].value[:1]) != "E" {
		t.Error("hemisphere references are wrong")
	}
	lat := readRational(gps[0x0002], 0) + readRational(gps[0x0002], 1)/60 + readRational(gps[0x0002], 2)/3600
	if math.Abs(lat-33.5) > 1e-4 {
		t.Errorf("latitude magnitude %v", lat)
	}
	if gps[0x0005].value[0] != 1 || readRational(gps[0x0006], 0) != 15 {
		t.Errorf("an altitude below sea level was written as ref %d, %v", gps[0x0005].value[0], readRational(gps[0x0006], 0))
	}
}

func TestBlockLeavesOutWhatWasNotGiven(t *testing.T) {
	block := Block(Tags{Model: "Only"})
	tiff := block[6:]
	root := directory(t, tiff, binary.LittleEndian.Uint32(tiff[4:]))
	if _, ok := root[0x010F]; ok {
		t.Error("an empty make was written")
	}
	gps := directory(t, tiff, binary.LittleEndian.Uint32(root[0x8825].value))
	if len(gps) != 0 {
		t.Errorf("null island produced a location: %v", gps)
	}
}

func TestAttachKeepsTheImageDecodable(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	withExif := Attach(buf.Bytes(), Tags{Model: "One", Latitude: 1, Longitude: 2})
	if !bytes.Equal(withExif[:4], []byte{0xFF, 0xD8, 0xFF, 0xE1}) {
		t.Fatal("the Exif segment is not straight after the start of image")
	}
	if _, err := jpeg.Decode(bytes.NewReader(withExif)); err != nil {
		t.Fatalf("the JPEG no longer decodes: %v", err)
	}
	for _, notJPEG := range [][]byte{nil, {0xFF}, []byte("GIF89a")} {
		if got := Attach(notJPEG, Tags{Model: "One"}); !bytes.Equal(got, notJPEG) {
			t.Errorf("Attach changed a non-JPEG: %q", got)
		}
	}
}

func TestSexagesimalRoundTrips(t *testing.T) {
	for _, degrees := range []float64{0, 0.5, 51.5074, -0.1278, 179.99999, -89.123456} {
		parts := sexagesimal(degrees)
		back := float64(parts[0][0]) + float64(parts[1][0])/60 + float64(parts[2][0])/float64(parts[2][1])/3600
		if math.Abs(back-math.Abs(degrees)) > 1e-5 {
			t.Errorf("%v came back as %v", degrees, back)
		}
	}
}
