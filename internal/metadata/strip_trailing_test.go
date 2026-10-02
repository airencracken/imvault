// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"

	"imvault/internal/exifwrite"
)

// leak is the marker every hostile fixture below hides somewhere. A stripped
// file containing it has shipped something it promised not to.
var leak = []byte("GPSLEAK-51.50140,-0.14189")

// exifSegment is an APP1 Exif segment carrying the leak marker.
func exifSegment() []byte {
	return segment(jpegAPP1, append([]byte("Exif\x00\x00MM\x00*\x00\x00\x00\x08"), leak...))
}

// appendedImage is a second JPEG with its own Exif, the shape a multi-picture
// file or a phone's depth map takes after the first image's EOI.
func appendedImage(t testing.TB) []byte {
	t.Helper()
	return insertAfterSOI(t, sampleJPEG(t), exifSegment())
}

func TestStripJPEGDropsEverythingAfterTheEndOfImage(t *testing.T) {
	base := sampleJPEG(t)
	trailers := map[string][]byte{
		"multi-picture image": appendedImage(t),
		"motion photo clip":   append([]byte("\x00\x00\x00\x18ftypmp42"), leak...),
		"plain bytes":         leak,
	}
	for name, trailer := range trailers {
		t.Run(name, func(t *testing.T) {
			data := append(append([]byte{}, base...), trailer...)
			out, ok := Strip(data, "image/jpeg")
			if !ok {
				t.Fatal("a JPEG with appended data was not handled")
			}
			if bytes.Contains(out, leak) {
				t.Fatalf("data after EOI survived (%d -> %d bytes)", len(data), len(out))
			}
			if !bytes.Equal(out, base) {
				t.Fatal("the primary image was not preserved exactly")
			}
			filtered, ok := Filter(data, "image/jpeg", false, false)
			if !ok || bytes.Contains(filtered, leak) {
				t.Fatal("data after EOI survived Filter")
			}
			for _, camera := range []bool{true, false} {
				if kept, ok := Filter(data, "image/jpeg", camera, !camera); ok && bytes.Contains(kept, leak) {
					t.Fatalf("data after EOI survived a partial filter (camera=%v)", camera)
				}
			}
		})
	}
}

func TestStripJPEGKeepsOnlyAllowedApplicationSegments(t *testing.T) {
	icc := segment(0xE2, append([]byte("ICC_PROFILE\x00\x01\x01"), []byte("colour")...))
	adobe := segment(0xEE, []byte("Adobe\x00\x64\x00\x00\x00\x00\x00"))
	jfif := segment(0xE0, []byte("JFIF\x00\x01\x02\x00\x00\x01\x00\x01\x00\x00"))
	dropped := [][]byte{
		segment(0xE2, append([]byte("MPF\x00"), leak...)),
		segment(0xEC, append([]byte("Ducky"), leak...)),
		segment(0xE3, leak),
		segment(0xE0, append([]byte("JFXX\x00"), leak...)),
		segment(0xED, append([]byte("Photoshop 3.0\x00"), leak...)),
		segment(0xFE, leak),
		exifSegment(),
	}
	data := sampleJPEG(t)
	for _, seg := range append(dropped, icc, adobe, jfif) {
		data = insertAfterSOI(t, data, seg)
	}
	out, ok := Strip(data, "image/jpeg")
	if !ok {
		t.Fatal("not handled")
	}
	if bytes.Contains(out, leak) {
		t.Fatal("a metadata application segment survived")
	}
	for _, kept := range [][]byte{icc, adobe, jfif} {
		if !bytes.Contains(out, kept) {
			t.Fatalf("a segment that affects decoding was dropped: %q", kept[4:12])
		}
	}
	decodeJPEG(t, out)
}

// progressiveJPEG is a synthetic multi-scan file: two scans separated by a
// table segment, with stuffed bytes, restart markers and fill bytes inside the
// entropy-coded data. It is not decodable, but every byte of structure that the
// stripper has to walk is real.
func progressiveJPEG() (data, want []byte) {
	scanHeader := segment(jpegSOS, []byte{1, 1, 0, 0, 0x3F, 0})
	firstScan := []byte{0x12, 0xFF, 0x00, 0x34, 0xFF, 0xD0, 0x56, 0xFF, 0xD1, 0x78}
	table := segment(0xC4, []byte{0x00, 1, 2, 3})
	secondScan := []byte{0x9A, 0xFF, 0x00, 0xBC, 0xFF, 0xD7, 0xDE}
	var b bytes.Buffer
	b.Write([]byte{0xFF, jpegSOI})
	b.Write(segment(0xDB, make([]byte, 65)))
	b.Write(scanHeader)
	b.Write(firstScan)
	b.Write(exifSegment()) // metadata between scans is still metadata
	b.Write(table)
	b.Write(scanHeader)
	b.Write(secondScan)
	b.Write([]byte{0xFF, 0xFF, 0xFF, jpegEOI}) // fill bytes before EOI
	want = append([]byte{0xFF, jpegSOI}, segment(0xDB, make([]byte, 65))...)
	want = append(want, scanHeader...)
	want = append(want, firstScan...)
	want = append(want, table...)
	want = append(want, scanHeader...)
	want = append(want, secondScan...)
	want = append(want, 0xFF, jpegEOI)
	return b.Bytes(), want
}

func TestStripJPEGWalksProgressiveScans(t *testing.T) {
	data, want := progressiveJPEG()
	data = append(data, appendedImage(t)...)
	out, ok := Strip(data, "image/jpeg")
	if !ok {
		t.Fatal("a multi-scan JPEG was not handled")
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("multi-scan JPEG stripped wrongly:\n got %x\nwant %x", out, want)
	}
}

func TestStripJPEGSuppliesAMissingEndOfImage(t *testing.T) {
	data := sampleJPEG(t)
	truncated := data[:len(data)-2] // drop the EOI
	out, ok := Strip(truncated, "image/jpeg")
	if !ok {
		t.Fatal("a JPEG missing its EOI was not handled")
	}
	if !bytes.Equal(out, data) {
		t.Fatal("the missing EOI was not supplied")
	}
}

func TestStripJPEGRefusesASecondStartOfImage(t *testing.T) {
	data := sampleJPEG(t)
	// A second SOI where a segment belongs is not a file this can vouch for.
	broken := append(append([]byte{0xFF, jpegSOI}, 0xFF, jpegSOI), data[2:]...)
	if _, ok := Strip(broken, "image/jpeg"); ok {
		t.Fatal("a nested start of image was accepted")
	}
}

func TestStripGIFDropsEverythingAfterTheTrailer(t *testing.T) {
	gif := minimalGIF()
	out, ok := Strip(append(append([]byte{}, gif...), leak...), "image/gif")
	if !ok {
		t.Fatal("not handled")
	}
	if !bytes.Equal(out, gif) {
		t.Fatalf("data after the GIF trailer survived: %q", out[len(gif)-1:])
	}
}

// minimalGIF is a one-pixel GIF with no global colour table.
func minimalGIF() []byte {
	gif := []byte("GIF89a")
	gif = append(gif, 1, 0, 1, 0, 0, 0, 0)
	gif = append(gif, 0x2C, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 2, 0x44, 0x01, 0)
	return append(gif, gifTrailer)
}

func TestStripWebPDropsBytesPastTheRIFFSize(t *testing.T) {
	webp := sampleWebP(t, false)
	data := append(append([]byte{}, webp...), webpChunk("ZZZZ", leak)...)
	out, ok := Strip(data, "image/webp")
	if !ok {
		t.Fatal("not handled")
	}
	if bytes.Contains(out, leak) {
		t.Fatal("a chunk past the RIFF size survived")
	}
}

func TestStripBMPDropsAppendedBytes(t *testing.T) {
	bmp := make([]byte, 58)
	copy(bmp, "BM")
	binary.LittleEndian.PutUint32(bmp[2:], uint32(len(bmp)))
	out, ok := Strip(append(append([]byte{}, bmp...), leak...), "image/bmp")
	if !ok || !bytes.Equal(out, bmp) {
		t.Fatal("bytes appended to a bitmap survived")
	}
	// A size of zero, as some old writers leave it, cannot be used to cut.
	binary.LittleEndian.PutUint32(bmp[2:], 0)
	if out, ok := Strip(bmp, "image/bmp"); !ok || !bytes.Equal(out, bmp) {
		t.Fatal("a bitmap with no declared size was not kept as it was")
	}
}

func TestCameraFilterDropsOwnerAndSerialNumbers(t *testing.T) {
	order := binary.LittleEndian
	ascii := func(s string) []byte { return append([]byte(s), 0) }
	values := map[uint16][]byte{
		0xa430: ascii("Owner Person"),
		0xa431: ascii("BODY-SERIAL-123"),
		0xa435: ascii("LENS-SERIAL-456"),
		0xa434: ascii("Lens Model 50mm"),
	}
	tags := []uint16{0x829a, 0xa430, 0xa431, 0xa434, 0xa435}
	data := make([]byte, 0, 256)
	data = append(data, 'I', 'I', 42, 0, 8, 0, 0, 0)
	data = order.AppendUint16(data, 1)
	data = append(data, make([]byte, 12)...)
	order.PutUint16(data[10:], 0x8769)
	order.PutUint16(data[12:], 4)
	order.PutUint32(data[14:], 1)
	data = append(data, 0, 0, 0, 0)
	child := len(data)
	order.PutUint32(data[18:], uint32(child))
	data = order.AppendUint16(data, uint16(len(tags)))
	entries := len(data)
	data = append(data, make([]byte, 12*len(tags)+4)...)
	for i, tag := range tags {
		at := entries + 12*i
		order.PutUint16(data[at:], tag)
		if tag == 0x829a {
			order.PutUint16(data[at+2:], 5)
			order.PutUint32(data[at+4:], 1)
			order.PutUint32(data[at+8:], uint32(len(data)))
			data = order.AppendUint32(order.AppendUint32(data, 1), 250)
			continue
		}
		order.PutUint16(data[at+2:], 2)
		order.PutUint32(data[at+4:], uint32(len(values[tag])))
		order.PutUint32(data[at+8:], uint32(len(data)))
		data = append(data, values[tag]...)
	}
	out := filteredTIFF(data, true)
	for _, secret := range []string{"Owner Person", "BODY-SERIAL-123", "LENS-SERIAL-456"} {
		if bytes.Contains(out, []byte(secret)) {
			t.Errorf("the camera filter kept %q", secret)
		}
	}
	if !bytes.Contains(out, []byte("Lens Model 50mm")) {
		t.Error("the camera filter dropped the lens model, which is not identifying")
	}
}

// hostileJPEG builds a JPEG from random combinations of metadata segments and
// trailers, every one of which carries the leak marker.
func hostileJPEG(t *testing.T, r *rand.Rand) []byte {
	t.Helper()
	carriers := [][]byte{
		exifSegment(),
		segment(0xE2, append([]byte("MPF\x00"), leak...)),
		segment(0xED, leak),
		segment(0xFE, leak),
		segment(0xEF, leak),
	}
	data := sampleJPEG(t)
	for n := r.Intn(4); n > 0; n-- {
		data = insertAfterSOI(t, data, carriers[r.Intn(len(carriers))])
	}
	switch r.Intn(3) {
	case 1:
		data = append(data, appendedImage(t)...)
	case 2:
		data = append(data, leak...)
	}
	return data
}

// Strip is idempotent and never ships the marker, however the metadata is
// arranged.
func TestStripPropertiesOverHostileJPEGs(t *testing.T) {
	r := rand.New(rand.NewSource(20261002))
	for i := 0; i < 500; i++ {
		data := hostileJPEG(t, r)
		out, ok := Strip(data, "image/jpeg")
		if !ok {
			t.Fatalf("case %d: not handled", i)
		}
		if bytes.Contains(out, leak) || bytes.Contains(out, []byte("Exif\x00\x00")) {
			t.Fatalf("case %d: metadata survived", i)
		}
		again, ok := Strip(out, "image/jpeg")
		if !ok || !bytes.Equal(out, again) {
			t.Fatalf("case %d: stripping is not idempotent", i)
		}
		decodeJPEG(t, out)
	}
}

func TestExifwriteRecordsAltitudeBelowSeaLevel(t *testing.T) {
	for _, altitude := range []float64{-15, 15} {
		block := exifwrite.Block(exifwrite.Tags{Latitude: 1, Longitude: 2, Altitude: altitude})[6:]
		gps := tiffLocation(block)
		if gps.altitude == nil || *gps.altitude != altitude {
			t.Fatalf("altitude %v read back as %v", altitude, gps.altitude)
		}
	}
}

func FuzzStrip(f *testing.F) {
	t := testing.TB(f)
	f.Add(sampleJPEG(t))
	f.Add(append(sampleJPEG(t), appendedImage(t)...))
	progressive, _ := progressiveJPEG()
	f.Add(progressive)
	f.Add(samplePNG(t))
	f.Add(minimalGIF())
	f.Add(append(minimalGIF(), leak...))
	f.Add([]byte("BM\x10\x00\x00\x00"))
	f.Add([]byte("RIFF\xff\xff\xff\xffWEBPVP8X"))
	f.Add([]byte{0xFF, 0xD8, 0xFF, 0xDA, 0x00, 0x02, 0xFF})
	f.Fuzz(func(t *testing.T, data []byte) {
		out, ok := Strip(data, "")
		if !ok {
			return
		}
		if len(out) > len(data)+2 {
			t.Fatalf("stripping grew the file from %d to %d bytes", len(data), len(out))
		}
		again, ok := Strip(out, "")
		if !ok || !bytes.Equal(out, again) {
			t.Fatal("stripping is not idempotent")
		}
		if looksLikeJPEG(out) && jpegHasSegment(out, jpegAPP1) {
			t.Fatal("an APP1 segment survived")
		}
		filtered, ok := Filter(data, "", false, false)
		if !ok || !bytes.Equal(filtered, out) {
			t.Fatal("Filter with nothing kept disagrees with Strip")
		}
		for _, camera := range []bool{true, false} {
			if kept, ok := Filter(data, "", camera, !camera); ok {
				if _, ok := Strip(kept, ""); !ok {
					t.Fatal("a filtered file can no longer be stripped")
				}
			}
		}
	})
}

// jpegHasSegment reports whether a JPEG's segment list contains a marker,
// walking scans the same way a decoder would.
func jpegHasSegment(data []byte, want byte) bool {
	for i := 2; i < len(data); {
		marker, next, ok := readJPEGMarker(data, i)
		if !ok || marker == jpegEOI {
			return false
		}
		i = next
		if marker >= 0xD0 && marker <= 0xD7 || marker == 0x01 {
			continue
		}
		if marker == want {
			return true
		}
		if i+2 > len(data) {
			return false
		}
		i += int(binary.BigEndian.Uint16(data[i:]))
		if marker == jpegSOS {
			i, _ = jpegScanEnd(data, i)
		}
	}
	return false
}
