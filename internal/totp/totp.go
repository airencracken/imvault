// SPDX-License-Identifier: AGPL-3.0-or-later

// Package totp implements the time-based one-time passwords of RFC 6238, which
// is what authenticator apps speak.
//
// It is deliberately small and dependency-free: HMAC-SHA1 over a counter, which
// is short enough to read and check against the RFC.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Digits is the code length every authenticator assumes.
	Digits = 6
	// Period is how long a code stays valid.
	Period = 30 * time.Second
	// SecretBytes is the secret size RFC 4226 recommends: 160 bits.
	SecretBytes = 20
	// skew is how many periods either side of now are accepted, to tolerate a
	// clock that is slightly off.
	skew = 1
)

// encoding is unpadded base32, which is what authenticator apps expect.
var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret returns a new random secret, base32 encoded.
func GenerateSecret() (string, error) {
	buf := make([]byte, SecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("totp: generate secret: %w", err)
	}
	return encoding.EncodeToString(buf), nil
}

// Normalise tidies user input: authenticator apps show secrets in groups of
// four with spaces, and people paste them that way.
func Normalise(secret string) string {
	secret = strings.ToUpper(strings.TrimSpace(secret))
	secret = strings.ReplaceAll(secret, " ", "")
	secret = strings.ReplaceAll(secret, "-", "")
	return strings.TrimRight(secret, "=")
}

// Code returns the code for a secret at a given moment.
func Code(secret string, at time.Time) (string, error) {
	key, err := decode(secret)
	if err != nil {
		return "", err
	}
	return format(hotp(key, uint64(at.Unix()/int64(Period.Seconds())))), nil
}

// Match reports which time step a code belongs to, if any.
//
// The step is returned rather than a bare boolean so a caller can refuse a code
// it has already accepted: without that, a code observed over somebody's
// shoulder stays usable for the rest of its window.
func Match(secret, code string, at time.Time) (step uint64, ok bool) {
	code = strings.TrimSpace(code)
	if len(code) != Digits {
		return 0, false
	}

	key, err := decode(secret)
	if err != nil {
		return 0, false
	}

	current := uint64(at.Unix() / int64(Period.Seconds()))

	for offset := -skew; offset <= skew; offset++ {
		candidate := int64(current) + int64(offset)
		if candidate < 0 {
			continue
		}

		expected := format(hotp(key, uint64(candidate)))
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return uint64(candidate), true
		}
	}

	return 0, false
}

// Verify reports whether a code is valid, without replay protection. Callers
// that can remember the last accepted step should use Match instead.
func Verify(secret, code string, at time.Time) bool {
	_, ok := Match(secret, code, at)
	return ok
}

// ProvisioningURI builds the otpauth:// URI that an authenticator app turns
// into an entry, or into a QR code.
func ProvisioningURI(issuer, account, secret string) string {
	// The label is "issuer:account". A literal colon is permitted by the key
	// URI format, but escaping it and the account is what every authenticator
	// in practice is tested against.
	label := escapeLabel(issuer) + "%3A" + escapeLabel(account)

	params := url.Values{}
	params.Set("secret", Normalise(secret))
	params.Set("issuer", issuer)
	params.Set("algorithm", "SHA1")
	params.Set("digits", fmt.Sprintf("%d", Digits))
	params.Set("period", fmt.Sprintf("%d", int(Period.Seconds())))

	return "otpauth://totp/" + label + "?" + params.Encode()
}

// escapeLabel percent-encodes a label component. PathEscape leaves ':' and '@'
// alone, since both are legal in a path segment, but they are the two
// characters most likely to be misread inside an otpauth label.
func escapeLabel(value string) string {
	escaped := url.PathEscape(value)
	escaped = strings.ReplaceAll(escaped, ":", "%3A")
	escaped = strings.ReplaceAll(escaped, "@", "%40")
	return escaped
}

// GroupSpaced renders a secret in groups of four, which is how people read one
// off a screen when they have to type it in.
func GroupSpaced(secret string) string {
	secret = Normalise(secret)
	var parts []string
	for len(secret) > 4 {
		parts = append(parts, secret[:4])
		secret = secret[4:]
	}
	if secret != "" {
		parts = append(parts, secret)
	}
	return strings.Join(parts, " ")
}

// hotp is the HMAC-based construction TOTP is built on, with the dynamic
// truncation of RFC 4226 section 5.3.
func hotp(key []byte, counter uint64) uint32 {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	return value % pow10(Digits)
}

func pow10(n int) uint32 {
	result := uint32(1)
	for i := 0; i < n; i++ {
		result *= 10
	}
	return result
}

func format(value uint32) string {
	return fmt.Sprintf("%0*d", Digits, value)
}

func decode(secret string) ([]byte, error) {
	key, err := encoding.DecodeString(Normalise(secret))
	if err != nil {
		return nil, fmt.Errorf("totp: secret is not valid base32: %w", err)
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("totp: secret is empty")
	}
	return key, nil
}
