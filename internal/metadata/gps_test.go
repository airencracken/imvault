// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// gpsBlock is a minimal TIFF with a root and GPS directory. Both byte orders
// and value placements describe exactly the same location.
func gpsBlock(order binary.ByteOrder, valuesFirst bool) []byte {
	data := make([]byte, 160)
	copy(data, "II")
	if order == binary.BigEndian {
		copy(data, "MM")
	}
	order.PutUint16(data[2:], 42)
	order.PutUint32(data[4:], 8)
	order.PutUint16(data[8:], 1)
	gps, lat, lon, alt := 26, 104, 128, 152
	if valuesFirst {
		gps, lat, lon, alt = 82, 26, 50, 74
	}
	entry := func(at int, tag, kind uint16, count, value uint32) {
		order.PutUint16(data[at:], tag)
		order.PutUint16(data[at+2:], kind)
		order.PutUint32(data[at+4:], count)
		order.PutUint32(data[at+8:], value)
	}
	entry(10, 0x8825, 4, 1, uint32(gps))
	order.PutUint16(data[gps:], 6)
	entry(gps+2, 1, 2, 2, 0)
	data[gps+10] = 'S'
	entry(gps+14, 2, 5, 3, uint32(lat))
	entry(gps+26, 3, 2, 2, 0)
	data[gps+34] = 'W'
	entry(gps+38, 4, 5, 3, uint32(lon))
	entry(gps+50, 5, 1, 1, 0)
	data[gps+58] = 1 // below sea level
	entry(gps+62, 6, 5, 1, uint32(alt))
	for offset, pairs := range map[int][]uint32{
		lat: {33, 1, 30, 1, 0, 1}, lon: {18, 1, 15, 1, 0, 1}, alt: {15, 1},
	} {
		for i, value := range pairs {
			order.PutUint32(data[offset+i*4:], value)
		}
	}
	return data
}

func TestGPSByteOrderAndValuePlacement(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for _, valuesFirst := range []bool{false, true} {
			data := insertAfterSOI(t, sampleJPEG(t), segment(0xE1, append([]byte("Exif\x00\x00"), gpsBlock(order, valuesFirst)...)))
			gps, handled := jpegLocation(bytes.NewReader(data))
			if !handled || gps.latitude == nil || gps.longitude == nil || gps.altitude == nil {
				t.Fatalf("%s valuesFirst=%t: incomplete location", order, valuesFirst)
			}
			if *gps.latitude != -33.5 || *gps.longitude != -18.25 || *gps.altitude != -15 {
				t.Fatalf("incorrect coordinates: %v, %v (%v m)", *gps.latitude, *gps.longitude, *gps.altitude)
			}
		}
	}
}

func TestGPSRejectsIncompleteAndMalformedLocations(t *testing.T) {
	order := binary.LittleEndian
	for name, mutate := range map[string]func([]byte){
		"missing longitude": func(b []byte) { order.PutUint16(b[64:], 99) },
		"wrong reference":   func(b []byte) { b[36] = 'X' },
		"wrong type":        func(b []byte) { order.PutUint16(b[42:], 3) },
		"wrong count":       func(b []byte) { order.PutUint32(b[44:], 2) },
		"invalid fix": func(b []byte) {
			order.PutUint16(b[76:], 9)
			order.PutUint16(b[78:], 2)
			order.PutUint32(b[80:], 2)
			b[84] = 'V'
		},
		"zero denominator": func(b []byte) { order.PutUint32(b[108:], 0) },
		"latitude range":   func(b []byte) { order.PutUint32(b[104:], 91) },
		"minutes range":    func(b []byte) { order.PutUint32(b[112:], 60) },
		"offset overflow":  func(b []byte) { order.PutUint32(b[48:], math.MaxUint32) },
		"root overflow":    func(b []byte) { order.PutUint32(b[4:], math.MaxUint32) },
		"GPS overflow":     func(b []byte) { order.PutUint32(b[18:], math.MaxUint32) },
		"directory count":  func(b []byte) { order.PutUint16(b[26:], math.MaxUint16) },
	} {
		t.Run(name, func(t *testing.T) {
			data := gpsBlock(order, false)
			mutate(data)
			gps := tiffLocation(data)
			if gps.latitude != nil || gps.longitude != nil || gps.altitude != nil {
				t.Fatal("malformed GPS produced a location")
			}
		})
	}
}

func TestGPSKeepsZeroCoordinatesWhenPresent(t *testing.T) {
	data := gpsBlock(binary.LittleEndian, false)
	for _, offset := range []int{104, 112, 120, 128, 136, 144} {
		binary.LittleEndian.PutUint32(data[offset:], 0)
	}
	gps := tiffLocation(data)
	if gps.latitude == nil || gps.longitude == nil || *gps.latitude != 0 || *gps.longitude != 0 {
		t.Fatal("explicit zero coordinates were confused with absent tags")
	}
}

func FuzzTIFFLocation(f *testing.F) {
	f.Add(gpsBlock(binary.LittleEndian, false))
	f.Add(gpsBlock(binary.BigEndian, true))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		// Also exercise the container scanner on malformed input.
		jpegLocation(bytes.NewReader(data))
		gps := tiffLocation(data)
		if (gps.latitude == nil) != (gps.longitude == nil) {
			t.Fatal("partial location")
		}
		if gps.latitude != nil && (math.Abs(*gps.latitude) > 90 || math.Abs(*gps.longitude) > 180) {
			t.Fatal("out-of-range location")
		}
	})
}
