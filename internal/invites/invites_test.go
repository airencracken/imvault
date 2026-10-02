// SPDX-License-Identifier: AGPL-3.0-or-later

package invites

import (
	"strings"
	"testing"

	"imvault/internal/apikeys"
)

func TestGeneratedCodesSplitAndVerify(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code := Generate()
		if !strings.HasPrefix(code.Full, Scheme+"_") {
			t.Fatalf("%q does not carry the scheme", code.Full)
		}
		prefix, ok := Split(code.Full)
		if !ok || prefix != code.Prefix {
			t.Fatalf("Split(%q) = %q, %v", code.Full, prefix, ok)
		}
		if !Verify(code.Full, code.Hash) {
			t.Fatalf("%q did not verify", code.Full)
		}
		if seen[code.Prefix] {
			t.Fatalf("a prefix repeated after %d codes", i)
		}
		seen[code.Prefix] = true
	}
}

// An API key has the same shape with a different scheme. It must never be
// accepted where an invitation is expected, or the other way round.
func TestInvitationsAndAPIKeysCannotStandInForEachOther(t *testing.T) {
	key := apikeys.Generate()
	if _, ok := Split(key.Full); ok {
		t.Fatal("an API key was read as an invitation")
	}
	code := Generate()
	if _, ok := apikeys.Split(code.Full); ok {
		t.Fatal("an invitation was read as an API key")
	}
}

func TestVerifyRefusesAnotherCodesHash(t *testing.T) {
	a, b := Generate(), Generate()
	if Verify(a.Full, b.Hash) || Verify(b.Full, a.Hash) {
		t.Fatal("a code verified against another code's digest")
	}
	if Verify("", a.Hash) || Verify(a.Full, "") {
		t.Fatal("an empty value verified")
	}
}

func TestSplitRefusesHostileInput(t *testing.T) {
	code := Generate().Full
	for _, bad := range []string{
		"", "inv", "inv__", code + "\x00", strings.Repeat("inv_", 1000),
		strings.Replace(code, "inv_", "inv_../", 1), "inv_" + strings.Repeat("a", 12) + "_" + strings.Repeat("b", 31),
	} {
		if _, ok := Split(bad); ok {
			t.Errorf("Split accepted %q", bad)
		}
	}
}
