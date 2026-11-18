// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"encoding/binary"
	"testing"
	"testing/quick"
)

func TestClassifyISOStillAndVideoBrands(t *testing.T) {
	for _, test := range []struct {
		brands string
		want   Format
	}{
		{"heic", FormatHEIF},
		{"mif1", FormatHEIF},
		{"mif1avif", FormatAVIF},
		{"avis", FormatAVIF},
		{"isomheix", FormatHEIF},
		{"iso8mp42", FormatMP4},
		{"qt  ", FormatMOV},
	} {
		head := make([]byte, 12+len(test.brands))
		binary.BigEndian.PutUint32(head[:4], uint32(len(head)))
		copy(head[4:], "ftyp")
		copy(head[8:], test.brands[:4])
		copy(head[16:], test.brands[4:])
		for _, input := range [][]byte{head, append(append([]byte{}, head...), "heicavif"...)} {
			got := Classify(input)
			if got != test.want || got.IsVideo() != test.want.IsVideo() {
				t.Errorf("%q: got %s, want %s", test.brands, got, test.want)
			}
		}
	}
	extended := []byte("\x00\x00\x00\x01ftyp\x00\x00\x00\x00\x00\x00\x00\x18heic\x00\x00\x00\x00")
	if got := Classify(extended); got != FormatHEIF {
		t.Fatalf("extended ftyp: %s", got)
	}
}

func TestClassifyISOBoxSizeProperty(t *testing.T) {
	err := quick.Check(func(size uint32, tail []byte) bool {
		head := []byte("\x00\x00\x00\x00ftypisom\x00\x00\x00\x00")
		binary.BigEndian.PutUint32(head[:4], 16)
		head = append(head, "heic"...)
		head = append(head, tail...)
		// The first box's declared end must stop a still brand in the next
		// box from changing our video classification.
		if Classify(head) != FormatMP4 {
			return false
		}
		binary.BigEndian.PutUint32(head[:4], size)
		return Classify(head) != ""
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}
