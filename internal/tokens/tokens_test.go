// SPDX-License-Identifier: AGPL-3.0-or-later

package tokens

import (
	"strings"
	"testing"
)

func TestNewReturnsTheTokenAndItsDigest(t *testing.T) {
	token, hash := New()
	if len(token) != 64 || hash != Hash(token) || hash == token {
		t.Fatalf("New() = %q, %q", token, hash)
	}
	// Known answer, computed independently with Python's hashlib.
	if got := Hash("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("Hash is not hex SHA-256: %s", got)
	}
}

func TestPrefixedTokensRoundTrip(t *testing.T) {
	generated := NewPrefixed("test")
	parts := strings.Split(generated.Full, "_")
	if len(parts) != 3 || parts[0] != "test" || parts[1] != generated.Prefix {
		t.Fatalf("unexpected shape %q", generated.Full)
	}
	if generated.Hash != Hash(generated.Full) {
		t.Fatal("the stored hash is not the digest of the whole token")
	}
	prefix, ok := SplitPrefixed("test", generated.Full)
	if !ok || prefix != generated.Prefix {
		t.Fatalf("SplitPrefixed = %q, %v", prefix, ok)
	}
	if !VerifyPrefixed(generated.Full, generated.Hash) {
		t.Fatal("a fresh token did not verify")
	}
}

func TestSplitPrefixedRejectsAnythingElse(t *testing.T) {
	valid := NewPrefixed("test").Full
	prefix, secret := strings.Split(valid, "_")[1], strings.Split(valid, "_")[2]
	for _, bad := range []string{
		"",
		"test",
		"test__",
		"other_" + prefix + "_" + secret,
		"TEST_" + prefix + "_" + secret,
		"test_" + prefix[1:] + "_" + secret,
		"test_" + prefix + "_" + secret + "x",
		"test_" + prefix + "_" + secret + "_extra",
		" " + valid,
		valid + "\n",
	} {
		if _, ok := SplitPrefixed("test", bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestVerifyPrefixedRejectsNearMisses(t *testing.T) {
	generated := NewPrefixed("test")
	flipped := []byte(generated.Full)
	flipped[len(flipped)-1] ^= 1
	for _, presented := range []string{"", string(flipped), generated.Full + "x", strings.ToUpper(generated.Full)} {
		if VerifyPrefixed(presented, generated.Hash) {
			t.Errorf("%q verified", presented)
		}
	}
	if VerifyPrefixed(generated.Full, "") || VerifyPrefixed(generated.Full, generated.Full) {
		t.Fatal("a token verified against something that is not its digest")
	}
}

func FuzzSplitPrefixed(f *testing.F) {
	f.Add(NewPrefixed("test").Full)
	f.Add("test_a_b")
	f.Fuzz(func(t *testing.T, full string) {
		prefix, ok := SplitPrefixed("test", full)
		if !ok {
			return
		}
		if len(prefix) != prefixLen || !strings.HasPrefix(full, "test_"+prefix+"_") || len(full) != len("test_")+prefixLen+1+secretLen {
			t.Fatalf("accepted a malformed token %q", full)
		}
	})
}
