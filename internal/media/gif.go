// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bufio"
	"fmt"
	"io"
)

// GIF block markers.
const (
	gifTrailer      = 0x3b
	gifExtension    = 0x21
	gifImageDesc    = 0x2c
	gifColorTable   = 0x80
	gifColorTableLn = 0x07
)

// maxGIFFrames bounds the scan so a malformed file cannot spin forever.
const maxGIFFrames = 100_000

// gifFrameCount counts a GIF's image descriptors without decoding any frames.
//
// Decoding every frame with gif.DecodeAll would be far more expensive and would
// hold the whole animation in memory; walking the block structure only reads
// the parts we need to skip over.
func gifFrameCount(src io.ReadSeeker) (int, error) {
	if err := rewind(src); err != nil {
		return 0, err
	}
	br := bufio.NewReaderSize(src, 32*1024)

	header := make([]byte, 6)
	if _, err := io.ReadFull(br, header); err != nil {
		return 0, fmt.Errorf("read gif header: %w", err)
	}
	if string(header) != "GIF87a" && string(header) != "GIF89a" {
		return 0, fmt.Errorf("not a gif")
	}

	// Logical screen descriptor, followed by an optional global colour table.
	descriptor := make([]byte, 7)
	if _, err := io.ReadFull(br, descriptor); err != nil {
		return 0, fmt.Errorf("read gif descriptor: %w", err)
	}
	if descriptor[4]&gifColorTable != 0 {
		skip := 3 * (1 << (uint(descriptor[4]&gifColorTableLn) + 1))
		if _, err := io.CopyN(io.Discard, br, int64(skip)); err != nil {
			return 0, fmt.Errorf("skip global colour table: %w", err)
		}
	}

	frames := 0
	for frames <= maxGIFFrames {
		marker, err := br.ReadByte()
		if err != nil {
			// A truncated file still tells us whether it was animated.
			return frames, nil
		}

		switch marker {
		case gifTrailer:
			return frames, nil

		case gifExtension:
			// 0x21 <label> <length-prefixed sub-blocks...> 0x00
			if _, err := br.ReadByte(); err != nil {
				return frames, nil
			}
			if err := skipSubBlocks(br); err != nil {
				return frames, nil
			}

		case gifImageDesc:
			frames++

			desc := make([]byte, 9)
			if _, err := io.ReadFull(br, desc); err != nil {
				return frames, nil
			}
			if desc[8]&gifColorTable != 0 {
				skip := 3 * (1 << (uint(desc[8]&gifColorTableLn) + 1))
				if _, err := io.CopyN(io.Discard, br, int64(skip)); err != nil {
					return frames, nil
				}
			}
			// LZW minimum code size, then the compressed image data.
			if _, err := br.ReadByte(); err != nil {
				return frames, nil
			}
			if err := skipSubBlocks(br); err != nil {
				return frames, nil
			}

		default:
			return frames, fmt.Errorf("unexpected gif block 0x%02x", marker)
		}
	}

	return frames, nil
}

// skipSubBlocks consumes a chain of length-prefixed data sub-blocks, which is
// how GIF delimits variable-length payloads.
func skipSubBlocks(br *bufio.Reader) error {
	for {
		n, err := br.ReadByte()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if _, err := io.CopyN(io.Discard, br, int64(n)); err != nil {
			return err
		}
	}
}
