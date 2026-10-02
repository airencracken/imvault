// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

// Bytes at or above the largest multiple of the alphabet's length are thrown
// away rather than folded onto its first letters.
func TestRecoveryCodesRejectBiasedBytes(t *testing.T) {
	limit := 256 - 256%len(recoveryAlphabet)
	// Every byte that would bias the draw, then enough good ones.
	var source bytes.Buffer
	for b := limit; b < 256; b++ {
		source.WriteByte(byte(b))
	}
	for i := 0; i < recoveryCodeLength; i++ {
		source.WriteByte(byte(i))
	}
	code, err := recoveryCodeFrom(&source)
	if err != nil {
		t.Fatal(err)
	}
	if code != "ABCDE-FGHJK" {
		t.Errorf("code = %q, want the unbiased bytes only", code)
	}

	// Running out of randomness is an error, not a short code.
	if _, err := recoveryCodeFrom(bytes.NewReader([]byte{255, 255})); err == nil {
		t.Error("an exhausted source produced a code")
	}
}

// Over many codes every letter turns up about equally often.
func TestRecoveryCodesAreUniform(t *testing.T) {
	counts := map[rune]int{}
	const codes = 3000
	for i := 0; i < codes; i++ {
		code, err := generateRecoveryCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != recoveryCodeLength+1 || code[recoveryCodeLength/2] != '-' {
			t.Fatalf("malformed code %q", code)
		}
		for _, r := range strings.ReplaceAll(code, "-", "") {
			if !strings.ContainsRune(recoveryAlphabet, r) {
				t.Fatalf("code %q uses %q, outside the alphabet", code, r)
			}
			counts[r]++
		}
	}
	expected := float64(codes*recoveryCodeLength) / float64(len(recoveryAlphabet))
	var chi float64
	for _, r := range recoveryAlphabet {
		diff := float64(counts[r]) - expected
		chi += diff * diff / expected
	}
	// 29 degrees of freedom; 70 is far beyond any honest draw.
	if chi > 70 || math.IsNaN(chi) {
		t.Errorf("letter frequencies are skewed: chi-square %.1f", chi)
	}
}
