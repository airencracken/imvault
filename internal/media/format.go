// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"encoding/binary"
)

// Format is the container/encoding an upload appears to be.
type Format string

const (
	// FormatImage is anything the image pipeline should try to decode.
	FormatImage Format = "image"
	// FormatGIF is a GIF, possibly animated (frame count decides).
	FormatGIF Format = "gif"
	// FormatWebP is a still WebP.
	FormatWebP Format = "webp"
	// FormatAnimatedWebP is a WebP carrying the animation flag.
	FormatAnimatedWebP Format = "animated-webp"
	// FormatWebM is a WebM/Matroska container.
	FormatWebM Format = "webm"
	// FormatMP4 is an ISO base media container.
	FormatMP4 Format = "mp4"
	// FormatMOV is a QuickTime container.
	FormatMOV Format = "mov"
)

// IsVideo reports whether the format is a video container.
func (f Format) IsVideo() bool {
	switch f {
	case FormatWebM, FormatMP4, FormatMOV:
		return true
	default:
		return false
	}
}

// Mime returns the content type to store for the format.
func (f Format) Mime() string {
	switch f {
	case FormatGIF:
		return "image/gif"
	case FormatWebP, FormatAnimatedWebP:
		return "image/webp"
	case FormatWebM:
		return "video/webm"
	case FormatMP4:
		return "video/mp4"
	case FormatMOV:
		return "video/quicktime"
	default:
		return ""
	}
}

// Ext returns the canonical file extension for the format.
func (f Format) Ext() string {
	switch f {
	case FormatGIF:
		return "gif"
	case FormatWebP, FormatAnimatedWebP:
		return "webp"
	case FormatWebM:
		return "webm"
	case FormatMP4:
		return "mp4"
	case FormatMOV:
		return "mov"
	default:
		return ""
	}
}

// maxChunkSize guards the WebP chunk walk against absurd lengths.
const maxChunkSize = 1 << 30

// Classify inspects the leading bytes of an upload and reports what it looks
// like. Detection is intentionally conservative: anything unrecognised is
// treated as a still image and validated by the image decoder, which rejects
// non-images.
func Classify(head []byte) Format {
	switch {
	case hasPrefix(head, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return FormatWebM

	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		if string(head[8:12]) == "qt  " {
			return FormatMOV
		}
		return FormatMP4

	case hasPrefix(head, []byte("GIF87a")), hasPrefix(head, []byte("GIF89a")):
		return FormatGIF

	case isWebP(head):
		if isAnimatedWebP(head) {
			return FormatAnimatedWebP
		}
		return FormatWebP

	default:
		return FormatImage
	}
}

// HeadSize is how many leading bytes Classify needs to make a decision.
const HeadSize = 512

func hasPrefix(b, prefix []byte) bool {
	return len(b) >= len(prefix) && string(b[:len(prefix)]) == string(prefix)
}

func isWebP(head []byte) bool {
	return len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WEBP"
}

// isAnimatedWebP walks the RIFF chunk list looking for VP8X, whose first flag
// byte carries the animation bit.
func isAnimatedWebP(head []byte) bool {
	const (
		animFlag = 0x02
	)

	for off := 12; off+8 <= len(head); {
		id := string(head[off : off+4])
		size := int(binary.LittleEndian.Uint32(head[off+4 : off+8]))
		if size < 0 || size > maxChunkSize {
			return false
		}

		if id == "VP8X" {
			return off+8 < len(head) && head[off+8]&animFlag != 0
		}

		// Chunks are padded to an even length.
		off += 8 + size + (size & 1)
	}
	return false
}
