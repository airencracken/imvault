// SPDX-License-Identifier: AGPL-3.0-or-later

package ids

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var base36 = regexp.MustCompile(`^[0-9a-z]+$`)

func TestNewUsesTheAlphabetAndLength(t *testing.T) {
	for _, length := range []int{1, 4, 12, 44} {
		id := New(length)
		if len(id) != length || !base36.MatchString(id) {
			t.Errorf("New(%d) = %q", length, id)
		}
	}
	for _, length := range []int{0, -5} {
		if id := New(length); len(id) != 12 {
			t.Errorf("New(%d) = %q, want the 12-character default", length, id)
		}
	}
}

func TestNewDoesNotRepeat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		id := New(12)
		if seen[id] {
			t.Fatalf("New repeated %q after %d draws", id, i)
		}
		seen[id] = true
	}
}

// Every symbol should turn up: a modulo bias or an off-by-one in the alphabet
// would show as a symbol that never appears.
func TestNewUsesEverySymbol(t *testing.T) {
	counts := map[rune]int{}
	for i := 0; i < 2000; i++ {
		for _, r := range New(12) {
			counts[r]++
		}
	}
	for _, r := range alphabet {
		if counts[r] == 0 {
			t.Errorf("%q never appeared", r)
		}
	}
}

func TestTokenIsHexOfTheRequestedEntropy(t *testing.T) {
	hex := regexp.MustCompile(`^[0-9a-f]+$`)
	for n, want := range map[int]int{16: 32, 32: 64, 0: 64, -1: 64} {
		if token := Token(n); len(token) != want || !hex.MatchString(token) {
			t.Errorf("Token(%d) = %q", n, token)
		}
	}
	if Token(32) == Token(32) {
		t.Fatal("two tokens were equal")
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Summer Holiday":           "summer-holiday",
		"  Beach -- 2026!! ":       "beach-2026",
		"Café au lait":             "café-au-lait",
		"../../etc/passwd":         "etc-passwd",
		"tabs\tand\nnewlines\x00x": "tabs-and-newlines-x",
		"ALLCAPS":                  "allcaps",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	for _, empty := range []string{"", "   ", "!!!", "--", "\x00\x01"} {
		if got := Slug(empty); len(got) != 8 || !base36.MatchString(got) {
			t.Errorf("Slug(%q) = %q, want a random 8-character fallback", empty, got)
		}
	}
}

var slugShape = regexp.MustCompile(`^[\p{L}\p{N}]+(-[\p{L}\p{N}]+)*$`)

func FuzzSlug(f *testing.F) {
	for _, seed := range []string{"Summer Holiday", strings.Repeat("ab ", 100), "../..", "\xff\xfe", "日本 語"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := Slug(in)
		if !utf8.ValidString(got) || !slugShape.MatchString(got) {
			t.Fatalf("Slug(%q) = %q is not a clean slug", in, got)
		}
		// Stopping after 60 bytes may overshoot by at most one rune.
		if len(got) > 60+utf8.UTFMax {
			t.Fatalf("Slug(%q) is %d bytes long", in, len(got))
		}
		if strings.ContainsAny(got, "/\\.") {
			t.Fatalf("Slug(%q) = %q could be read as a path", in, got)
		}
	})
}
