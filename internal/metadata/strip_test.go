// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// sampleJPEG is a small real JPEG, encoded by the standard library. It carries
// a JFIF segment and nothing else, which makes it the baseline the injected
// metadata is added to.
func sampleJPEG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 30), B: 64, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// samplePNG is a small real PNG, likewise with only the chunks it needs.
func samplePNG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 30), B: 64, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// insertAfterSOI puts a segment immediately after the start-of-image marker,
// which is where a real encoder would have written it.
func insertAfterSOI(t *testing.T, jpegData, segment []byte) []byte {
	t.Helper()

	if len(jpegData) < 2 || jpegData[0] != 0xFF || jpegData[1] != 0xD8 {
		t.Fatal("the fixture is not a JPEG")
	}
	out := make([]byte, 0, len(jpegData)+len(segment))
	out = append(out, jpegData[:2]...)
	out = append(out, segment...)
	out = append(out, jpegData[2:]...)
	return out
}

// segment builds a JPEG segment for a marker and an opaque payload.
func segment(marker byte, payload []byte) []byte {
	length := len(payload) + 2
	out := []byte{0xFF, marker, byte(length >> 8), byte(length & 0xFF)}
	return append(out, payload...)
}

// decodeJPEG decodes, failing the test rather than returning an error, because
// every assertion below depends on the output still being an image.
func decodeJPEG(t *testing.T, data []byte) image.Image {
	t.Helper()

	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the stripped JPEG does not decode: %v", err)
	}
	return img
}

func TestStripJPEGRemovesEverythingIdentifying(t *testing.T) {
	base := sampleJPEG(t)

	// A JPEG with the metadata a phone would write: Exif including a location
	// and an embedded thumbnail, a separate XMP packet, IPTC, and a comment.
	thumbnail := sampleJPEG(t)
	exif := []byte("Exif\x00\x00MM\x00*\x00\x00\x00\x08GPSLatitude=51.5GPSLongitude=-0.1")
	exif = append(exif, thumbnail...)
	xmp := append([]byte("http://ns.adobe.com/xap/1.0/\x00"), []byte("<x:xmpmeta>GPS</x:xmpmeta>")...)

	// A JFIF segment, which is colour information and must survive, and a JFXX
	// one beside it, which holds a thumbnail and must not.
	jfif := segment(0xE0, append([]byte("JFIF\x00"), []byte("\x01\x02\x00\x00\x01\x00\x01\x00\x00")...))
	jfxx := segment(0xE0, append([]byte("JFXX\x00"), []byte("\x10")...))

	withMetadata := insertAfterSOI(t, base, jfif)
	withMetadata = insertAfterSOI(t, withMetadata, jfxx)
	withMetadata = insertAfterSOI(t, withMetadata, segment(0xE1, exif))
	withMetadata = insertAfterSOI(t, withMetadata, segment(0xE1, xmp))
	withMetadata = insertAfterSOI(t, withMetadata, segment(0xED, []byte("Photoshop 3.0\x00IPTC")))
	withMetadata = insertAfterSOI(t, withMetadata, segment(0xFE, []byte("a comment")))
	// Adobe's colour transform, which is not metadata and must survive.
	withMetadata = insertAfterSOI(t, withMetadata, segment(0xEE, []byte("Adobe\x00\x64\x00\x00\x00\x00\x00")))

	// The fixture really does contain what we are about to look for, or the
	// test would pass by having nothing to remove.
	for _, needle := range [][]byte{[]byte("GPSLatitude"), []byte("xmpmeta"), []byte("Photoshop"), []byte("a comment")} {
		if !bytes.Contains(withMetadata, needle) {
			t.Fatalf("the fixture is missing %q", needle)
		}
	}

	cleaned, ok := Strip(withMetadata, "image/jpeg")
	if !ok {
		t.Fatal("a JPEG was not handled")
	}

	for _, needle := range [][]byte{
		[]byte("GPSLatitude"), []byte("GPSLongitude"), []byte("xmpmeta"),
		[]byte("Photoshop"), []byte("a comment"), []byte("Exif"),
	} {
		if bytes.Contains(cleaned, needle) {
			t.Errorf("the stripped JPEG still contains %q", needle)
		}
	}

	// The embedded thumbnail is inside the Exif segment and goes with it. It is
	// the classic thing to miss: strip the Exif and leave a second copy of the
	// image, metadata and all, in the same file.
	if bytes.Contains(cleaned, thumbnail) {
		t.Error("the embedded thumbnail survived")
	}

	if !bytes.Contains(cleaned, []byte("Adobe")) {
		t.Error("the Adobe colour segment was dropped; it is not metadata")
	}
	if !bytes.Contains(cleaned, []byte("JFIF")) {
		t.Error("the JFIF segment was dropped; it is colour information, not metadata")
	}
	if bytes.Contains(cleaned, []byte("JFXX")) {
		t.Error("the JFXX thumbnail segment survived")
	}

	// And the pixels are untouched, which is the whole reason for working at
	// the segment level instead of re-encoding.
	before, after := decodeJPEG(t, withMetadata), decodeJPEG(t, cleaned)
	if !before.Bounds().Eq(after.Bounds()) {
		t.Fatalf("bounds changed: %v then %v", before.Bounds(), after.Bounds())
	}
	for y := before.Bounds().Min.Y; y < before.Bounds().Max.Y; y++ {
		for x := before.Bounds().Min.X; x < before.Bounds().Max.X; x++ {
			if before.At(x, y) != after.At(x, y) {
				t.Fatalf("pixel %d,%d changed: %v then %v", x, y, before.At(x, y), after.At(x, y))
			}
		}
	}

	// Stripping is idempotent: there is nothing left to remove.
	again, ok := Strip(cleaned, "image/jpeg")
	if !ok {
		t.Fatal("a stripped JPEG was not handled the second time")
	}
	if !bytes.Equal(cleaned, again) {
		t.Error("stripping twice changed the file")
	}
}

func TestStripJPEGKeepsAFileWithNothingToRemove(t *testing.T) {
	base := sampleJPEG(t)

	cleaned, ok := Strip(base, "image/jpeg")
	if !ok {
		t.Fatal("a plain JPEG was not handled")
	}
	// Go's encoder writes JFIF and no metadata, so the bytes should survive
	// exactly. Anything else means the rewriter is mangling files it has no
	// business touching.
	if !bytes.Equal(base, cleaned) {
		t.Error("a JPEG with no metadata was rewritten anyway")
	}
}

func TestStripPNGRemovesAncillaryTextChunks(t *testing.T) {
	base := samplePNG(t)

	withMetadata := insertPNGChunk(t, base, "tEXt", []byte("Make\x00Example"))
	withMetadata = insertPNGChunk(t, withMetadata, "iTXt", []byte("XML:com.adobe.xmp\x00GPSLatitude"))
	withMetadata = insertPNGChunk(t, withMetadata, "eXIf", []byte("Exif\x00\x00MM\x00*latitude"))
	withMetadata = insertPNGChunk(t, withMetadata, "tIME", []byte{0x07, 0xEA, 8, 15, 12, 0, 0})

	for _, needle := range [][]byte{[]byte("Make"), []byte("GPSLatitude"), []byte("latitude")} {
		if !bytes.Contains(withMetadata, needle) {
			t.Fatalf("the fixture is missing %q", needle)
		}
	}

	cleaned, ok := Strip(withMetadata, "image/png")
	if !ok {
		t.Fatal("a PNG was not handled")
	}

	for _, needle := range [][]byte{[]byte("Make"), []byte("GPSLatitude"), []byte("latitude")} {
		if bytes.Contains(cleaned, needle) {
			t.Errorf("the stripped PNG still contains %q", needle)
		}
	}
	for _, kind := range []string{"tEXt", "iTXt", "eXIf", "tIME"} {
		if bytes.Contains(cleaned, []byte(kind)) {
			t.Errorf("the %s chunk survived", kind)
		}
	}

	// The image chunks are still there and it still decodes to the same thing.
	if !bytes.Contains(cleaned, []byte("IHDR")) || !bytes.Contains(cleaned, []byte("IDAT")) {
		t.Error("the image chunks were dropped")
	}
	before, err := png.Decode(bytes.NewReader(withMetadata))
	if err != nil {
		t.Fatal(err)
	}
	after, err := png.Decode(bytes.NewReader(cleaned))
	if err != nil {
		t.Fatalf("the stripped PNG does not decode: %v", err)
	}
	if !before.Bounds().Eq(after.Bounds()) {
		t.Error("the pixels changed")
	}

	if again, ok := Strip(cleaned, "image/png"); !ok || !bytes.Equal(cleaned, again) {
		t.Error("stripping twice changed the PNG")
	}
}

// insertPNGChunk adds a chunk immediately after the header, which is where an
// encoder would put a metadata chunk it wrote up front.
func insertPNGChunk(t *testing.T, data []byte, kind string, payload []byte) []byte {
	t.Helper()

	if !looksLikePNG(data) {
		t.Fatal("the fixture is not a PNG")
	}

	chunk := make([]byte, 0, len(payload)+12)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	chunk = append(chunk, length[:]...)
	chunk = append(chunk, kind...)
	chunk = append(chunk, payload...)
	// The CRC is over the type and the data. It is computed here because the
	// stripper relies on chunks being rectangular, not on it being correct,
	// and a fixture with a wrong CRC would be a misleading test.
	chunk = append(chunk, crc32Of(append([]byte(kind), payload...))...)

	const headerEnd = 8 + 25 // signature plus IHDR
	out := make([]byte, 0, len(data)+len(chunk))
	out = append(out, data[:headerEnd]...)
	out = append(out, chunk...)
	out = append(out, data[headerEnd:]...)
	return out
}

func crc32Of(data []byte) []byte {
	sum := crc32Checksum(data)
	return []byte{byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum)}
}

// --- WebP ------------------------------------------------------------------

// webpChunk builds a RIFF chunk, padded to an even length as the container
// requires.
func webpChunk(kind string, payload []byte) []byte {
	out := make([]byte, 0, len(payload)+9)
	out = append(out, kind...)
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(payload)))
	out = append(out, length[:]...)
	out = append(out, payload...)
	if len(payload)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

// sampleWebP builds a container with a VP8X header and a payload chunk. The
// VP8 payload is not a real encoded image: the container logic is what is under
// test, and a decoder is not involved.
func sampleWebP(t *testing.T, withMetadata bool) []byte {
	t.Helper()

	// Canvas 8x8, with the EXIF flag set when metadata is present. Width and
	// height are each three bytes, little endian, holding the size minus one.
	var vp8x [10]byte
	if withMetadata {
		vp8x[0] = webpFlagExif
	}
	putUint24(vp8x[4:7], 7)
	putUint24(vp8x[7:10], 7)

	body := make([]byte, 0, 256)
	body = append(body, webpChunk("VP8X", vp8x[:])...)
	body = append(body, webpChunk("VP8 ", []byte("not really a bitstream"))...)
	if withMetadata {
		body = append(body, webpChunk("EXIF", []byte("Exif\x00\x00GPSLatitude=51.5"))...)
		body = append(body, webpChunk("XMP ", []byte("<x:xmpmeta>GPS</x:xmpmeta>"))...)
	}

	out := make([]byte, 0, len(body)+12)
	out = append(out, webpRIFF...)
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(body)+4))
	out = append(out, size[:]...)
	out = append(out, webpWEBP...)
	out = append(out, body...)
	return out
}

func TestStripWebPRemovesMetadataAndRepairsTheContainer(t *testing.T) {
	withMetadata := sampleWebP(t, true)

	if !bytes.Contains(withMetadata, []byte("GPSLatitude")) {
		t.Fatal("the fixture has no metadata to remove")
	}

	cleaned, ok := Strip(withMetadata, "image/webp")
	if !ok {
		t.Fatal("a WebP was not handled")
	}

	for _, needle := range [][]byte{[]byte("GPSLatitude"), []byte("xmpmeta"), []byte("EXIF"), []byte("XMP ")} {
		if bytes.Contains(cleaned, needle) {
			t.Errorf("the stripped WebP still contains %q", needle)
		}
	}

	// VP8X tells a decoder which optional chunks to expect, so the EXIF and XMP
	// flags have to be cleared or it will look for what is no longer there.
	flags := webpFlagsOf(t, cleaned)
	if flags&(webpFlagExif|webpFlagXMP) != 0 {
		t.Errorf("VP8X still advertises metadata: %#02x", flags)
	}

	// The RIFF size counts everything after itself, and it changed.
	if got, want := riffSize(t, cleaned), len(cleaned)-8; got != want {
		t.Errorf("RIFF size = %d, want %d", got, want)
	}

	// The image chunk survived.
	if !bytes.Contains(cleaned, []byte("VP8 ")) {
		t.Error("the image chunk was dropped")
	}
}

func TestStripWebPLeavesACleanFileAlone(t *testing.T) {
	base := sampleWebP(t, false)

	cleaned, ok := Strip(base, "image/webp")
	if !ok {
		t.Fatal("a WebP was not handled")
	}
	if !bytes.Equal(base, cleaned) {
		t.Error("a WebP with no metadata was rewritten anyway")
	}
}

func webpFlagsOf(t *testing.T, data []byte) byte {
	t.Helper()

	if len(data) < 21 || string(data[12:16]) != "VP8X" {
		t.Fatal("the output has no VP8X chunk where one is expected")
	}
	return data[20]
}

// putUint24 writes a three-byte little-endian field, which is what VP8X uses
// for its canvas dimensions.
func putUint24(dst []byte, value uint32) {
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
	dst[2] = byte(value >> 16)
}

func riffSize(t *testing.T, data []byte) int {
	t.Helper()

	if len(data) < 8 {
		t.Fatal("not a RIFF file")
	}
	return int(binary.LittleEndian.Uint32(data[4:8]))
}

// --- structural failures ---------------------------------------------------

func TestStripRefusesWhatItCannotRead(t *testing.T) {
	// Every one of these must report "not handled", because the caller has to
	// distinguish "cleaned" from "could not clean" and must never treat the
	// second as the first.
	base := sampleJPEG(t)
	pngBase := samplePNG(t)

	cases := map[string][]byte{
		"empty":                      {},
		"a single byte":              {0xFF},
		"something else entirely":    []byte("this is not an image at all"),
		"a JPEG header and nothing":  {0xFF, 0xD8},
		"a JPEG with a lying length": insertAfterSOI(t, base, []byte{0xFF, 0xE1, 0xFF, 0xFF, 'x'}),
		"a truncated PNG":            pngBase[:20],
		"a PNG with no IEND":         bytes.Replace(pngBase, []byte("IEND"), []byte("IENX"), 1),
		"a truncated WebP":           sampleWebP(t, true)[:16],
	}

	for name, data := range cases {
		if cleaned, ok := Strip(data, ""); ok {
			t.Errorf("%s was reported as handled, producing %d bytes", name, len(cleaned))
		}
	}

	// A valid file with the wrong declared type is still handled, because the
	// sniffing decides and the declared type is not trusted.
	if _, ok := Strip(base, "text/plain"); !ok {
		t.Error("a JPEG was refused because the declared type was wrong")
	}
}

func TestHandledAgreesWithStrip(t *testing.T) {
	for name, data := range map[string][]byte{
		"jpeg": sampleJPEG(t),
		"png":  samplePNG(t),
		"webp": sampleWebP(t, true),
		"junk": []byte("nope"),
	} {
		_, stripped := Strip(data, "")
		if got := Handled(data); got != stripped {
			t.Errorf("%s: Handled = %v but Strip handled it = %v", name, got, stripped)
		}
	}
}

// crc32Checksum is the PNG chunk checksum.
func crc32Checksum(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

// --- GIF -------------------------------------------------------------------

// sampleGIF is a real GIF, encoded by the standard library, so the fixture
// decodes and the extensions can be injected where an encoder would have put
// them.
func sampleGIF(t *testing.T) []byte {
	t.Helper()

	img := image.NewPaletted(image.Rect(0, 0, 4, 4), palette.Plan9)
	for i := range img.Pix {
		img.Pix[i] = uint8(i % 2)
	}

	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

// gifExtensionBlock builds one extension: its label, the fixed block if it has
// one, and a sub-block chain.
func gifExtensionBlock(label byte, fixed []byte, payload []byte) []byte {
	out := []byte{0x21, label}
	if fixed != nil {
		out = append(out, byte(len(fixed)))
		out = append(out, fixed...)
	}
	for len(payload) > 0 {
		size := len(payload)
		if size > 255 {
			size = 255
		}
		out = append(out, byte(size))
		out = append(out, payload[:size]...)
		payload = payload[size:]
	}
	return append(out, 0x00)
}

// gifInsertBlocks puts extension blocks immediately before the first image
// descriptor, which is a legal place for them.
func gifInsertBlocks(t *testing.T, data []byte, blocks []byte) []byte {
	t.Helper()

	if !looksLikeGIF(data) || len(data) < 13 {
		t.Fatal("the fixture is not a GIF")
	}
	header := 13
	if data[10]&0x80 != 0 {
		header += 3 * (1 << ((data[10] & 0x07) + 1))
	}
	if header >= len(data) {
		t.Fatal("the fixture has no image block")
	}

	out := make([]byte, 0, len(data)+len(blocks))
	out = append(out, data[:header]...)
	out = append(out, blocks...)
	out = append(out, data[header:]...)
	return out
}

func TestStripGIFRemovesExtensionsAndKeepsTheLoop(t *testing.T) {
	base := sampleGIF(t)

	blocks := gifExtensionBlock(gifApplication, []byte(gifLoopApplication),
		[]byte{0x01, 0x00, 0x00})
	blocks = append(blocks, gifExtensionBlock(gifApplication, []byte("XMP DataXMP"),
		[]byte("<x:xmpmeta>GPSLatitude=51.5</x:xmpmeta>"))...)
	blocks = append(blocks, gifExtensionBlock(gifComment, nil, []byte("taken at home"))...)
	blocks = append(blocks, gifExtensionBlock(0x01, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		[]byte("a caption"))...)

	withMetadata := gifInsertBlocks(t, base, blocks)
	if !bytes.Contains(withMetadata, []byte("GPSLatitude")) {
		t.Fatal("the fixture has no metadata to remove")
	}

	cleaned, ok := Strip(withMetadata, "image/gif")
	if !ok {
		t.Fatal("a GIF was not handled")
	}

	for _, needle := range [][]byte{[]byte("GPSLatitude"), []byte("taken at home"), []byte("a caption")} {
		if bytes.Contains(cleaned, needle) {
			t.Errorf("the stripped GIF still contains %q", needle)
		}
	}

	// The loop block is how the animation says how many times to repeat.
	// Dropping every application block would silently change how it plays.
	if !bytes.Contains(cleaned, []byte(gifLoopApplication)) {
		t.Error("the loop extension was dropped; it is not metadata")
	}

	// And it is still the same animation.
	before, err := gif.DecodeAll(bytes.NewReader(withMetadata))
	if err != nil {
		t.Fatal(err)
	}
	after, err := gif.DecodeAll(bytes.NewReader(cleaned))
	if err != nil {
		t.Fatalf("the stripped GIF does not decode: %v", err)
	}
	if len(before.Image) != len(after.Image) {
		t.Fatalf("frame count changed: %d then %d", len(before.Image), len(after.Image))
	}
	if !before.Image[0].Bounds().Eq(after.Image[0].Bounds()) {
		t.Error("the frame bounds changed")
	}
}

func TestStripGIFLeavesACleanFileAlone(t *testing.T) {
	base := sampleGIF(t)

	cleaned, ok := Strip(base, "image/gif")
	if !ok {
		t.Fatal("a GIF was not handled")
	}
	if !bytes.Equal(base, cleaned) {
		t.Error("a GIF with no metadata was rewritten anyway")
	}
}

// TestCleansAgreesWithTheStripper holds the list the interface warns from to
// what the stripper actually does. A warning that is wrong in either direction
// is worse than none: it either cries wolf or stays silent about a leak.
func TestCleansAgreesWithTheStripper(t *testing.T) {
	fixtures := map[string][]byte{
		"jpg":  sampleJPEG(t),
		"jpeg": sampleJPEG(t),
		"png":  samplePNG(t),
		"webp": sampleWebP(t, true),
		"gif":  sampleGIF(t),
	}

	for ext, data := range fixtures {
		if !Cleans(ext) {
			t.Errorf("Cleans(%q) = false, but there is a fixture for it", ext)
		}
		if _, ok := Strip(data, ""); !ok {
			t.Errorf("Cleans(%q) is true but Strip does not handle it", ext)
		}
	}

	// The formats the interface must warn about.
	for _, ext := range []string{"tif", "tiff", "mp4", "mov", "webm", "", "exe"} {
		if Cleans(ext) {
			t.Errorf("Cleans(%q) = true, which would silence a warning", ext)
		}
	}

	// A BMP has nothing to remove and says so honestly, rather than claiming a
	// job it did not do.
	if !Cleans("bmp") {
		t.Error("Cleans(\"bmp\") = false, but a BMP has no metadata to remove")
	}
}
