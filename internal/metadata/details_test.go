// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"imvault/internal/exifwrite"
)

// jpegWithExif is a JPEG carrying the fields a phone would write, including a
// location. The block itself is built by internal/exifwrite, which the sample
// media generator uses as well, so there is one implementation of "how a camera
// writes Exif" rather than two.
func jpegWithExif(t *testing.T) []byte {
	t.Helper()

	return exifwrite.Attach(sampleJPEG(t), exifwrite.Tags{
		Make:        "TestCam",
		Model:       "TestCam One",
		Software:    "imvault test suite",
		Artist:      "A Photographer",
		Copyright:   "(c) 2026 A Photographer",
		LensModel:   "TestLens 50mm",
		Taken:       "2026:09:21 14:30:00",
		Exposure:    [2]uint32{1, 250},
		Aperture:    [2]uint32{28, 10},
		FocalLength: [2]uint32{50, 1},
		ISO:         200,
		Latitude:    51.5074,
		Longitude:   -0.1273,
		Altitude:    35,
	})
}

func TestExtractReadsWhatAPhoneWouldWrite(t *testing.T) {
	details, err := Extract(bytes.NewReader(jpegWithExif(t)))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if details == nil {
		t.Fatal("no details were found in a file that has them")
	}

	for _, tc := range []struct{ name, got, want string }{
		{"camera", details.Camera, "TestCam One"},
		{"lens", details.Lens, "TestLens 50mm"},
		{"software", details.Software, "imvault test suite"},
		{"artist", details.Artist, "A Photographer"},
		{"copyright", details.Copyright, "(c) 2026 A Photographer"},
		{"taken", details.Taken, "21 September 2026 at 14:30"},
		{"iso", details.ISO, "200"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	// The make is not repeated, because the model already begins with it.
	if strings.Contains(details.Camera, "TestCam TestCam") {
		t.Errorf("camera = %q, which repeats the make", details.Camera)
	}

	// Exposure, aperture, and focal length come out through the library's own
	// formatting, which is what knows 1/250 is not "0.004".
	if !strings.Contains(details.Exposure, "1/250") {
		t.Errorf("exposure = %q, want 1/250", details.Exposure)
	}
	if !strings.Contains(details.Aperture, "2.8") {
		t.Errorf("aperture = %q, want f/2.8", details.Aperture)
	}
	if !strings.Contains(details.Focal, "50") {
		t.Errorf("focal length = %q, want 50mm", details.Focal)
	}

	if details.Latitude == nil || details.Longitude == nil {
		t.Fatal("the location was not read")
	}
	if math.Abs(*details.Latitude-51.5074) > 0.001 {
		t.Errorf("latitude = %v, want about 51.5074", *details.Latitude)
	}
	// West is negative, which is the thing a hand-written parser gets wrong.
	if math.Abs(*details.Longitude-(-0.1273)) > 0.001 {
		t.Errorf("longitude = %v, want about -0.1273", *details.Longitude)
	}
	if details.Altitude == nil || math.Abs(*details.Altitude-35) > 0.1 {
		t.Errorf("altitude = %v, want 35", details.Altitude)
	}
}

func TestExtractGPSValuesBeforeTheirDirectory(t *testing.T) {
	// TIFF offsets are relative to the header, not constrained to point
	// forward. Move the GPS directory after its values without moving them.
	block := exifwrite.Block(exifwrite.Tags{
		Model: "TestCam One", Latitude: 51.5074, Longitude: -0.1273,
	})
	tiff := block[6:]
	order := binary.LittleEndian
	root := int(order.Uint32(tiff[4:8]))
	count := int(order.Uint16(tiff[root:]))
	found := false
	for i := 0; i < count; i++ {
		entry := tiff[root+2+i*12:][:12]
		if order.Uint16(entry) != 0x8825 {
			continue
		}
		offset := int(order.Uint32(entry[8:]))
		size := 2 + int(order.Uint16(tiff[offset:]))*12 + 4
		order.PutUint32(entry[8:], uint32(len(tiff)))
		block = append(block, tiff[offset:offset+size]...)
		found = true
		break
	}
	if !found {
		t.Fatal("fixture has no GPS directory")
	}
	data := insertAfterSOI(t, sampleJPEG(t), segment(0xE1, block))
	details, err := Extract(bytes.NewReader(data))
	if err != nil || details == nil {
		t.Fatalf("extract: %v, %v", details, err)
	}
	if got := details.Location(); got != "51.50740, -0.12730" {
		t.Fatalf("location = %q, want coordinates from before the directory", got)
	}
}

func TestExtractFindsNothingInAPlainImage(t *testing.T) {
	// A file with no metadata is not a failure, and must not be reported as one.
	details, err := Extract(bytes.NewReader(sampleJPEG(t)))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if details != nil {
		t.Errorf("details were invented: %+v", details)
	}
}

func TestStrippedFilesHaveNothingLeftToRead(t *testing.T) {
	// The two halves of this package have to agree: what the stripper removes
	// is what a reader can find, and nothing else. If stripping left something
	// behind, this is where it shows.
	withExif := jpegWithExif(t)

	before, err := Extract(bytes.NewReader(withExif))
	if err != nil || before == nil {
		t.Fatalf("the fixture is not readable: %v", err)
	}

	cleaned, ok := Strip(withExif, "image/jpeg")
	if !ok {
		t.Fatal("the fixture was not stripped")
	}

	after, err := Extract(bytes.NewReader(cleaned))
	if err == nil && after != nil {
		t.Errorf("metadata survived the stripper: %+v", after)
	}
}

func TestExtractSurvivesRubbish(t *testing.T) {
	// This runs on everything anybody uploads, so a parser that panics on a
	// malformed file would be a denial of service with an upload form.
	cases := map[string][]byte{
		"empty":            {},
		"not an image":     []byte("this is not a photograph"),
		"a lone JPEG":      {0xFF, 0xD8},
		"an Exif header":   append([]byte{0xFF, 0xD8}, segment(0xE1, []byte("Exif\x00\x00"))...),
		"a stub TIFF":      append(sampleJPEG(t)[:2], segment(0xE1, append([]byte("Exif\x00\x00"), []byte("II\x2A\x00\x08\x00\x00\x00")...))...),
		"truncated Exif":   jpegWithExif(t)[:40],
		"an empty segment": insertAfterSOI(t, sampleJPEG(t), segment(0xE1, nil)),
	}

	for name, data := range cases {
		// The only requirement is that it comes back rather than falls over.
		if _, err := Extract(bytes.NewReader(data)); err != nil {
			continue // an error is a fine answer for rubbish
		}
		t.Logf("%s: read without error", name)
	}
}

func TestDetailsRoundTrip(t *testing.T) {
	original, err := Extract(bytes.NewReader(jpegWithExif(t)))
	if err != nil || original == nil {
		t.Fatalf("extract: %v", err)
	}

	restored := DecodeDetails(original.Encode())
	if restored == nil {
		t.Fatal("the details did not survive encoding")
	}
	if restored.Camera != original.Camera || restored.Location() != original.Location() {
		t.Errorf("round trip changed the details: %+v then %+v", original, restored)
	}

	// Nothing has one representation rather than two.
	var empty Details
	if got := empty.Encode(); got != "" {
		t.Errorf("an empty set encoded to %q, want an empty string", got)
	}
	if got := DecodeDetails(""); got != nil {
		t.Error("an empty string decoded to something")
	}
	if got := DecodeDetails("{not json"); got != nil {
		t.Error("unreadable details decoded to something")
	}
}

func TestIdentifyingFieldsAreMarked(t *testing.T) {
	details, err := Extract(bytes.NewReader(jpegWithExif(t)))
	if err != nil || details == nil {
		t.Fatalf("extract: %v", err)
	}

	byLabel := map[string]Field{}
	for _, field := range details.Fields() {
		byLabel[field.Label] = field
	}

	// The camera and the date are what a family archive is for. Where it was
	// taken and who owns the camera are not.
	for _, label := range []string{"Location", "Artist", "Copyright"} {
		field, ok := byLabel[label]
		if !ok {
			t.Errorf("%s was not among the fields", label)
			continue
		}
		if !field.Identifying {
			t.Errorf("%s is not marked as identifying", label)
		}
	}
	for _, label := range []string{"Taken", "Camera", "Lens", "Exposure", "ISO"} {
		if field, ok := byLabel[label]; ok && field.Identifying {
			t.Errorf("%s is marked as identifying", label)
		}
	}

	if !details.HasIdentifying() {
		t.Error("a file with a location and an artist reports nothing identifying")
	}

	// And a set without any of them says so, so the page can stay quiet.
	plain := Details{Camera: "TestCam One"}
	if plain.HasIdentifying() {
		t.Error("a camera and nothing else reports something identifying")
	}
}

func TestCameraNameUsesTheIdentifiedMake(t *testing.T) {
	// The library normalises IFD0.Make in place while identifying it, so the
	// raw field is mangled for a camera it recognises. The identified name is
	// the one to use, which also makes it read properly.
	known := exifwrite.Attach(sampleJPEG(t), exifwrite.Tags{
		Make:  "Apple",
		Model: "iPhone 15 Pro",
	})
	details, err := Extract(bytes.NewReader(known))
	if err != nil || details == nil {
		t.Fatalf("extract: %v", err)
	}
	if details.Camera != "Apple iPhone 15 Pro" {
		t.Errorf("camera = %q, want \"Apple iPhone 15 Pro\"", details.Camera)
	}

	// A model that already begins with the make is not prefixed twice.
	repeated := exifwrite.Attach(sampleJPEG(t), exifwrite.Tags{
		Make:  "Canon",
		Model: "Canon EOS 5D",
	})
	details, err = Extract(bytes.NewReader(repeated))
	if err != nil || details == nil {
		t.Fatalf("extract: %v", err)
	}
	if details.Camera != "Canon EOS 5D" {
		t.Errorf("camera = %q, want \"Canon EOS 5D\"", details.Camera)
	}

	// An unknown make falls back to the model rather than to the mangled name.
	unknown := exifwrite.Attach(sampleJPEG(t), exifwrite.Tags{
		Make:  "Example Camera Co.",
		Model: "Example One",
	})
	details, err = Extract(bytes.NewReader(unknown))
	if err != nil || details == nil {
		t.Fatalf("extract: %v", err)
	}
	if details.Camera != "Example One" {
		t.Errorf("camera = %q, want \"Example One\"", details.Camera)
	}
}
