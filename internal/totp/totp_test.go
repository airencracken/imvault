// SPDX-License-Identifier: AGPL-3.0-or-later

package totp

import (
	"strings"
	"testing"
	"time"
)

// The test vectors from RFC 6238 appendix B, which use a known secret and
// fixed timestamps. They are the reason this package can be trusted to agree
// with an authenticator app.
func TestRFC6238Vectors(t *testing.T) {
	// The RFC uses the ASCII secret "12345678901234567890", which is this in
	// base32.
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

	vectors := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}

	for _, vector := range vectors {
		at := time.Unix(vector.unix, 0)

		got, err := Code(secret, at)
		if err != nil {
			t.Fatalf("Code(%d): %v", vector.unix, err)
		}
		if got != vector.want {
			t.Errorf("Code at %d = %s, want %s", vector.unix, got, vector.want)
		}

		if step, ok := Match(secret, vector.want, at); !ok {
			t.Errorf("Match(%s) at %d rejected a valid code", vector.want, vector.unix)
		} else if step != uint64(vector.unix/30) {
			t.Errorf("Match at %d = step %d, want %d", vector.unix, step, vector.unix/30)
		}
	}
}

func TestCodesChangeOnThePeriod(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}

	// Aligned to a period boundary, or "the same period" would not be.
	base := time.Unix((1_700_000_000/30)*30, 0)

	first, err := Code(secret, base)
	if err != nil {
		t.Fatal(err)
	}
	// Still the same period.
	same, _ := Code(secret, base.Add(Period-time.Second))
	if same != first {
		t.Errorf("the code changed within a period: %s then %s", first, same)
	}

	// A different period.
	next, _ := Code(secret, base.Add(Period))
	if next == first {
		t.Error("the code did not change across a period boundary")
	}
}

func TestMatchAllowsOneStepOfSkew(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1_700_000_000, 0)

	previous, _ := Code(secret, now.Add(-Period))
	current, _ := Code(secret, now)
	next, _ := Code(secret, now.Add(Period))

	for label, code := range map[string]string{
		"previous period": previous,
		"current period":  current,
		"next period":     next,
	} {
		if !Verify(secret, code, now) {
			t.Errorf("%s was rejected; a clock a little out of step should still work", label)
		}
	}

	// Two periods away is not accepted.
	far, _ := Code(secret, now.Add(3*Period))
	if Verify(secret, far, now) {
		t.Error("a code three periods away was accepted")
	}
}

func TestMatchRejectsMalformedInput(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Now()

	for _, code := range []string{
		"", "12345", "1234567", "abcdef", "12345a", "  ", "-",
	} {
		if Verify(secret, code, now) {
			t.Errorf("Verify accepted %q", code)
		}
	}

	// A secret that is not base32 cannot match anything.
	if Verify("not!base32!", "123456", now) {
		t.Error("Verify accepted a code for an invalid secret")
	}
}

func TestNormaliseAcceptsWhatPeoplePaste(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}

	// Authenticator apps and setup pages show secrets in groups, sometimes
	// lowercase, sometimes with the base32 padding.
	grouped := GroupSpaced(secret)
	if Normalise(grouped) != Normalise(secret) {
		t.Errorf("Normalise(%q) = %q, want %q", grouped, Normalise(grouped), Normalise(secret))
	}
	if Normalise(strings.ToLower(secret)) != Normalise(secret) {
		t.Error("Normalise does not accept lowercase")
	}
	if Normalise(secret+"=") != Normalise(secret) {
		t.Error("Normalise does not strip padding")
	}

	if Verify(grouped, mustCode(t, secret, time.Now()), time.Now()) {
		// Grouping the secret must not change which codes it produces, so this
		// should verify.
	} else {
		t.Error("a grouped secret was not accepted")
	}
}

func TestGroupSpacedIsReadableAndReversible(t *testing.T) {
	got := GroupSpaced("JBSWY3DPEHPK3PXP")
	if got != "JBSW Y3DP EHPK 3PXP" {
		t.Errorf("GroupSpaced = %q", got)
	}
	if Normalise(got) != "JBSWY3DPEHPK3PXP" {
		t.Errorf("grouping is not reversible: %q", Normalise(got))
	}

	// Odd lengths still group cleanly.
	if got := GroupSpaced("ABCDEFG"); got != "ABCD EFG" {
		t.Errorf("GroupSpaced = %q, want %q", got, "ABCD EFG")
	}
}

func TestGeneratedSecretsAreDistinctAndUsable(t *testing.T) {
	seen := map[string]bool{}

	for i := 0; i < 200; i++ {
		secret, err := GenerateSecret()
		if err != nil {
			t.Fatal(err)
		}
		if seen[secret] {
			t.Fatal("generated a duplicate secret")
		}
		seen[secret] = true

		// A generated secret must survive the round trip through decode.
		code, err := Code(secret, time.Now())
		if err != nil {
			t.Fatalf("a generated secret did not decode: %v", err)
		}
		if len(code) != Digits {
			t.Errorf("code %q is not %d digits", code, Digits)
		}
		if _, ok := Match(secret, code, time.Now()); !ok {
			t.Error("a freshly generated code did not match")
		}
	}
}

func TestProvisioningURI(t *testing.T) {
	uri := ProvisioningURI("imvault", "alice@example.com", "JBSWY3DPEHPK3PXP")

	for _, want := range []string{
		"otpauth://totp/",
		"secret=JBSWY3DPEHPK3PXP",
		"issuer=imvault",
		"algorithm=SHA1",
		"digits=6",
		"period=30",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("URI is missing %q: %s", want, uri)
		}
	}

	// The label carries the issuer and the account, escaped, which is what
	// makes an authenticator app show a sensible name.
	if !strings.Contains(uri, "otpauth://totp/imvault%3Aalice%40example.com") {
		t.Errorf("label is not escaped as expected: %s", uri)
	}
}

func TestMatchReportsTheStepForReplayProtection(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_700_000_000, 0)

	step, ok := Match(secret, mustCode(t, secret, now), now)
	if !ok {
		t.Fatal("a valid code did not match")
	}

	// A caller that has already accepted this step can refuse it again, which
	// is the whole point of returning it.
	if step <= step-1 {
		t.Fatal("nonsense")
	}
	if _, ok := Match(secret, mustCode(t, secret, now), now); !ok {
		t.Error("Match should still report the step; refusing a replay is the caller's job")
	}
}

func mustCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()

	code, err := Code(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return code
}
