// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ids generates the short opaque identifiers used as public file
// handles, plus helpers for deriving URL slugs.
package ids

import (
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"strings"
	"unicode"
)

// alphabet is lowercase base36. URLs stay tidy and case-insensitive-friendly.
const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// New returns a random base36 identifier of the given length.
func New(length int) string {
	if length <= 0 {
		length = 12
	}
	max := big.NewInt(int64(len(alphabet)))
	var b strings.Builder
	b.Grow(length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand failing is not recoverable in any useful way.
			panic("ids: crypto/rand unavailable: " + err.Error())
		}
		b.WriteByte(alphabet[n.Int64()])
	}
	return b.String()
}

// Token returns a hex-encoded random token of n random bytes.
func Token(n int) string {
	if n <= 0 {
		n = 32
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic("ids: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// Slug converts an arbitrary label into a URL-friendly slug.
func Slug(s string) string {
	var b strings.Builder
	pendingDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteRune(r)
		default:
			pendingDash = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = New(8)
	}
	return out
}
