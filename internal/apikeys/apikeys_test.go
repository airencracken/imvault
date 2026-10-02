// SPDX-License-Identifier: AGPL-3.0-or-later

package apikeys

import (
	"strings"
	"testing"
)

func TestGenerateProducesUsableKeys(t *testing.T) {
	seen := map[string]bool{}

	for i := 0; i < 200; i++ {
		gen := Generate()

		if !strings.HasPrefix(gen.Full, Scheme+"_") {
			t.Fatalf("key %q does not start with the scheme", gen.Full)
		}
		if gen.Hash == "" {
			t.Fatal("no hash produced")
		}

		prefix, ok := Split(gen.Full)
		if !ok {
			t.Fatalf("Split rejected a freshly generated key %q", gen.Full)
		}
		if prefix != gen.Prefix {
			t.Errorf("Split prefix = %q, want %q", prefix, gen.Prefix)
		}
		if !Verify(gen.Full, gen.Hash) {
			t.Errorf("Verify rejected a freshly generated key %q", gen.Full)
		}

		if seen[gen.Full] {
			t.Fatalf("generated a duplicate key after %d iterations", i)
		}
		seen[gen.Full] = true
	}
}

func TestSplitRejectsMalformed(t *testing.T) {
	valid := Generate().Full

	tests := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"too few parts", "imv_abc"},
		{"too many parts", valid + "_extra"},
		{"wrong scheme", strings.Replace(valid, "imv_", "xxx_", 1)},
		{"short prefix", "imv_abc_def"},
		{"truncated secret", valid[:len(valid)-1]},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := Split(tc.key); ok {
				t.Errorf("Split(%q) accepted a malformed key", tc.key)
			}
		})
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	a := Generate()
	b := Generate()

	if Verify(b.Full, a.Hash) {
		t.Error("a different key verified against the stored hash")
	}
	if Verify(a.Full, b.Hash) {
		t.Error("a key verified against another key's hash")
	}
	if Verify("", a.Hash) {
		t.Error("an empty key verified")
	}
	if Verify(a.Full, "") {
		t.Error("a key verified against an empty hash")
	}
}

func TestHashIsStable(t *testing.T) {
	// A known digest pins the algorithm and encoding: a stored hash written by
	// one release has to verify under the next. Comparing Hash with itself
	// would pass whatever it computed.
	// The digest was computed independently, with Python's hashlib.
	const key = "imv_abcdefghijkl_0123456789abcdefghijklmnopqrstuv"
	const want = "d1c122f275b40cf7328357bc4a2ae9605c394624153db419036a16d0c9c88ce3"
	if got := Hash(key); got != want {
		t.Fatalf("Hash(%q) = %s, want %s", key, got, want)
	}
	if Hash(key) == Hash(key+"x") {
		t.Error("Hash ignores the trailing character")
	}
}
