// SPDX-License-Identifier: AGPL-3.0-or-later

// Package metadata removes identifying metadata from stored media.
//
// It works at the container level rather than by re-encoding. The pixels are
// left byte-for-byte as they were, which is what lets the promise that
// originals are never re-encoded stay true while still not shipping somebody's
// coordinates. Every function here removes whole segments, chunks, or atoms, so
// nothing has to be re-serialised and no payload is ever rewritten.
//
// A false return means "I do not know how to clean this", never "there was
// nothing to clean". Callers must not treat unhandled content as safe.
package metadata

import (
	"encoding/binary"
	"strings"
)

// Strip returns content with its metadata removed, and whether the format was
// one this package handled.
//
// Whole segments are dropped, so the result decodes to exactly the same image.
// The one exception is a container that records which optional chunks it
// carries — WebP — where a flag is cleared so a decoder is not told to expect
// something that is no longer there.
func Strip(data []byte, mime string) ([]byte, bool) {
	switch {
	case looksLikeJPEG(data):
		return stripJPEG(data)
	case looksLikePNG(data):
		return stripPNG(data)
	case looksLikeWebP(data):
		return stripWebP(data)
	case looksLikeGIF(data):
		return stripGIF(data)
	case looksLikeBMP(data):
		// BMP has no Exif and no text chunks: there is genuinely nothing to
		// remove, which is different from not knowing how.
		return data, true
	}
	return nil, false
}

// Handled reports whether Strip can clean this content, by sniffing it rather
// than trusting the declared type.
//
// Video is deliberately absent. Its metadata lives in container atoms, and
// removing an atom shifts every absolute sample offset that follows it, so it
// is remuxed with ffmpeg instead of rewritten here.
func Handled(data []byte) bool {
	return looksLikeJPEG(data) || looksLikePNG(data) || looksLikeWebP(data) ||
		looksLikeGIF(data) || looksLikeBMP(data)
}

// --- JPEG ------------------------------------------------------------------

const (
	jpegSOI = 0xD8
	jpegEOI = 0xD9
	jpegSOS = 0xDA
	jpegCOM = 0xFE
	// APP1 carries Exif and XMP, APP13 carries IPTC and Photoshop's resource
	// block. Both are metadata end to end.
	jpegAPP1  = 0xE1
	jpegAPP13 = 0xED
	// APP14 records the colour transform an Adobe CMYK file needs. It is not
	// metadata and dropping it changes how the image decodes.
	jpegAPP14 = 0xEE
)

func looksLikeJPEG(data []byte) bool {
	return len(data) >= 2 && data[0] == 0xFF && data[1] == jpegSOI
}

// stripJPEG walks the segment list and drops the metadata ones, copying the
// entropy-coded scan verbatim.
func stripJPEG(data []byte) ([]byte, bool) {
	if !looksLikeJPEG(data) {
		return nil, false
	}

	out := make([]byte, 0, len(data))
	out = append(out, 0xFF, jpegSOI)

	i := 2
	for i < len(data) {
		// A segment starts with a marker, which is 0xFF followed by anything
		// that is not 0x00 or another 0xFF. Padding FF bytes are legal.
		if data[i] != 0xFF {
			return nil, false // not where a marker should be
		}
		for i < len(data) && data[i] == 0xFF {
			i++
		}
		if i >= len(data) {
			return nil, false
		}
		marker := data[i]
		i++

		// Standalone markers carry no length or payload.
		switch {
		case marker == jpegEOI:
			out = append(out, 0xFF, jpegEOI)
			return out, true
		case marker >= 0xD0 && marker <= 0xD7, marker == 0x01:
			out = append(out, 0xFF, marker)
			continue
		}

		if i+2 > len(data) {
			return nil, false
		}
		length := int(binary.BigEndian.Uint16(data[i:]))
		if length < 2 || i+length > len(data) {
			return nil, false
		}
		payload := data[i+2 : i+length]

		switch {
		case marker == jpegSOS:
			// Everything from the start of scan to the end of file is entropy
			// coded data, which cannot be parsed as segments and must be copied
			// exactly. That includes any EOI.
			out = append(out, 0xFF, jpegSOS)
			out = append(out, data[i:]...)
			return out, true

		case marker == jpegAPP1, marker == jpegAPP13, marker == jpegCOM:
			// Dropped. APP1 holds Exif and XMP along with Exif's own embedded
			// thumbnail, which is a second copy of the image and the classic
			// way to "strip metadata" and still ship the location.

		case marker == 0xE0 && hasPrefix(payload, "JFXX"):
			// A JFXX extension segment carries a thumbnail. The JFIF segment
			// beside it is colour information and is kept.

		default:
			out = append(out, 0xFF, marker)
			out = append(out, data[i:i+length]...)
		}

		i += length
	}

	// Ran out of data without a scan or an end marker.
	return nil, false
}

func hasPrefix(data []byte, prefix string) bool {
	if len(data) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if data[i] != prefix[i] {
			return false
		}
	}
	return true
}

// --- PNG -------------------------------------------------------------------

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}

// pngDropped are the ancillary chunks that describe rather than depict.
//
// tEXt, zTXt, and iTXt hold arbitrary text — camera make, software, sometimes
// coordinates. eXIf is Exif, and tIME is a modification timestamp.
//
// Everything else is kept, including chunks this package has never heard of:
// they may be colour management (iCCP, gAMA, sRGB, cHRM), animation control
// (acTL, fcTL, fdAT), or something newer, and none of them are ours to judge.
var pngDropped = map[string]bool{
	"tEXt": true,
	"zTXt": true,
	"iTXt": true,
	"eXIf": true,
	"tIME": true,
}

func looksLikePNG(data []byte) bool {
	if len(data) < len(pngSignature) {
		return false
	}
	for i := range pngSignature {
		if data[i] != pngSignature[i] {
			return false
		}
	}
	return true
}

// stripPNG drops whole chunks. Each chunk carries its own CRC over its type and
// data, so removing a chunk leaves every remaining one valid and no checksum
// has to be recomputed.
func stripPNG(data []byte) ([]byte, bool) {
	if !looksLikePNG(data) {
		return nil, false
	}

	out := make([]byte, 0, len(data))
	out = append(out, pngSignature...)

	i := len(pngSignature)
	for i < len(data) {
		if i+8 > len(data) {
			return nil, false
		}
		length := int(binary.BigEndian.Uint32(data[i:]))
		if length < 0 || i+12+length > len(data) {
			return nil, false
		}

		kind := string(data[i+4 : i+8])
		end := i + 12 + length

		if !pngDropped[kind] {
			out = append(out, data[i:end]...)
		}

		i = end
		if kind == "IEND" {
			// Anything after IEND is not part of the image.
			return out, true
		}
	}

	return nil, false // no IEND
}

// --- WebP ------------------------------------------------------------------

var (
	webpRIFF = []byte("RIFF")
	webpWEBP = []byte("WEBP")
)

// webpDropped are the chunks WebP reserves for exactly this and nothing else.
var webpDropped = map[string]bool{
	"EXIF": true,
	"XMP ": true,
}

// The VP8X flags, which tell a decoder which optional chunks to expect.
const (
	webpFlagExif = 0x08
	webpFlagXMP  = 0x04
)

func looksLikeWebP(data []byte) bool {
	return len(data) >= 12 && string(data[0:4]) == string(webpRIFF) &&
		string(data[8:12]) == string(webpWEBP)
}

// stripWebP drops the metadata chunks and repairs the two places the container
// records them: the file size in the RIFF header, and the flags in VP8X.
func stripWebP(data []byte) ([]byte, bool) {
	if !looksLikeWebP(data) {
		return nil, false
	}

	out := make([]byte, 0, len(data))
	out = append(out, data[0:12]...)

	i := 12
	for i < len(data) {
		if i+8 > len(data) {
			return nil, false
		}
		kind := string(data[i : i+4])
		length := int(binary.LittleEndian.Uint32(data[i+4:]))
		if length < 0 || i+8+length > len(data) {
			return nil, false
		}

		// Chunks are padded to an even length.
		padded := length + (length & 1)
		end := i + 8 + padded
		if end > len(data) {
			return nil, false
		}

		switch {
		case webpDropped[kind]:
			// Dropped.

		case kind == "VP8X" && length >= 1:
			chunk := append([]byte(nil), data[i:end]...)
			chunk[8] &^= webpFlagExif | webpFlagXMP
			out = append(out, chunk...)

		default:
			out = append(out, data[i:end]...)
		}

		i = end
	}

	if len(out) < 12 {
		return nil, false
	}
	// The RIFF size counts everything after the size field itself.
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	return out, true
}

// --- BMP -------------------------------------------------------------------

func looksLikeBMP(data []byte) bool {
	return len(data) >= 2 && data[0] == 'B' && data[1] == 'M'
}

// --- GIF -------------------------------------------------------------------

// The extension labels a GIF can carry.
const (
	gifExtension       = 0x21
	gifApplication     = 0xFF
	gifComment         = 0xFE
	gifPlainText       = 0x01
	gifGraphicControl  = 0xF9
	gifImageDescriptor = 0x2C
	gifTrailer         = 0x3B
)

// gifLoopApplication is the one application block that is not metadata: it is
// how an animation says how many times to repeat, and removing it changes how
// the image plays.
const gifLoopApplication = "NETSCAPE2.0"

func looksLikeGIF(data []byte) bool {
	return len(data) >= 6 && (string(data[0:6]) == "GIF87a" || string(data[0:6]) == "GIF89a")
}

// Cleans reports whether content with this extension can be cleaned.
//
// It is the list the interface warns from, so it has to agree with what Strip
// actually does; a test holds the two together.
func Cleans(ext string) bool {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "jpg", "jpeg", "png", "webp", "bmp", "gif":
		return true
	}
	return false
}

// stripGIF drops comment, plain text, and application extension blocks, keeping
// everything that is part of the picture.
//
// GIF carries metadata in extension blocks rather than in a header, and the
// awkward one is the application block: the loop count lives there too, so it
// is kept by name and every other application block is dropped. XMP is written
// as one, which is why "keep the extensions" is not an option.
func stripGIF(data []byte) ([]byte, bool) {
	if !looksLikeGIF(data) {
		return nil, false
	}

	// Header, logical screen descriptor, and the global colour table when the
	// flag says there is one.
	if len(data) < 13 {
		return nil, false
	}
	header := 13
	if data[10]&0x80 != 0 {
		header += 3 * (1 << ((data[10] & 0x07) + 1))
	}
	if header > len(data) {
		return nil, false
	}

	out := make([]byte, 0, len(data))
	out = append(out, data[:header]...)

	i := header
	for i < len(data) {
		switch data[i] {
		case gifTrailer:
			out = append(out, data[i:]...)
			return out, true

		case gifImageDescriptor:
			// An image block runs to the end of its LZW sub-block chain, and
			// all of it is picture.
			end, ok := skipGIFImage(data, i)
			if !ok {
				return nil, false
			}
			out = append(out, data[i:end]...)
			i = end

		case gifExtension:
			end, label, ok := scanGIFExtension(data, i)
			if !ok {
				return nil, false
			}
			keep := label == gifGraphicControl
			if label == gifApplication && gifApplicationName(data, i) == gifLoopApplication {
				keep = true
			}
			if keep {
				out = append(out, data[i:end]...)
			}
			i = end

		default:
			return nil, false // not somewhere a block can start
		}
	}

	return nil, false // no trailer
}

// gifApplicationName reads the eleven-byte identifier of an application block.
func gifApplicationName(data []byte, at int) string {
	const nameOffset = 3 // 0x21, 0xFF, block size
	if at+nameOffset+11 > len(data) {
		return ""
	}
	return string(data[at+nameOffset : at+nameOffset+11])
}

// scanGIFExtension returns the end of an extension block and its label.
func scanGIFExtension(data []byte, at int) (int, byte, bool) {
	if at+2 > len(data) {
		return 0, 0, false
	}
	label := data[at+1]

	i := at + 2
	// The application and plain text extensions are introduced by a fixed-size
	// block before their sub-block chain; the others go straight to it.
	switch label {
	case gifApplication, gifPlainText:
		if i >= len(data) {
			return 0, 0, false
		}
		i += int(data[i]) + 1
		if i > len(data) {
			return 0, 0, false
		}
	case gifComment, gifGraphicControl:
		if label == gifGraphicControl {
			if i >= len(data) {
				return 0, 0, false
			}
			i += int(data[i]) + 1
			if i > len(data) {
				return 0, 0, false
			}
		}
	default:
		return 0, 0, false
	}

	end, ok := skipGIFSubBlocks(data, i)
	if !ok {
		return 0, 0, false
	}
	return end, label, true
}

// skipGIFImage returns the end of an image block: its descriptor, an optional
// local colour table, and the LZW sub-block chain.
func skipGIFImage(data []byte, at int) (int, bool) {
	if at+10 > len(data) {
		return 0, false
	}
	i := at + 10
	if data[at+9]&0x80 != 0 { // local colour table present
		i += 3 * (1 << ((data[at+9] & 0x07) + 1))
	}
	if i >= len(data) {
		return 0, false
	}
	i++ // LZW minimum code size
	return skipGIFSubBlocks(data, i)
}

// skipGIFSubBlocks walks a chain of length-prefixed blocks to its terminator.
func skipGIFSubBlocks(data []byte, at int) (int, bool) {
	i := at
	for i < len(data) {
		size := int(data[i])
		i++
		if size == 0 {
			return i, true
		}
		i += size
		if i > len(data) {
			return 0, false
		}
	}
	return 0, false
}
