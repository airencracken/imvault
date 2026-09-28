// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestGPSRecoveredAfterUnrelatedEXIFError(t *testing.T) {
	// Keep a valid GPS directory but add an EXIF subdirectory pointer outside
	// the segment. A broken camera-settings directory must not hide valid GPS.
	block := gpsBlock(binary.LittleEndian, false)
	root := len(block)
	block = append(block, make([]byte, 30)...)
	binary.LittleEndian.PutUint32(block[4:], uint32(root))
	binary.LittleEndian.PutUint16(block[root:], 2)
	copy(block[root+2:], block[10:22])
	entry := block[root+14:]
	binary.LittleEndian.PutUint16(entry, 0x8769)
	binary.LittleEndian.PutUint16(entry[2:], 4)
	binary.LittleEndian.PutUint32(entry[4:], 1)
	binary.LittleEndian.PutUint32(entry[8:], 0xfffffff0)
	data := insertAfterSOI(t, sampleJPEG(t), segment(0xe1, append([]byte("Exif\x00\x00"), block...)))
	if _, err := decode(bytes.NewReader(data)); err == nil {
		t.Fatal("fixture should fail the general EXIF parser")
	}
	details, err := Extract(bytes.NewReader(data))
	if err != nil || details.Location() != "-33.50000, -18.25000 (-15m)" {
		t.Fatalf("valid GPS lost after camera-settings error: %v, %v", details, err)
	}
}
