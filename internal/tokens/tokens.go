// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tokens mints opaque secrets and hashes them for storage.
//
// Sessions, API keys and auth tokens all follow the same shape: a high-entropy
// string handed to the client, of which only a digest is kept. Keeping that in
// one place means there is one implementation to get right.
package tokens

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"imvault/internal/ids"
)

// New returns a fresh token and the hash to store alongside it.
func New() (token, hash string) {
	token = ids.Token(32)
	return token, Hash(token)
}

// Hash returns the storage digest for a token.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Prefixed is a token shaped "<scheme>_<prefix>_<secret>".
//
// The prefix is stored in the clear and indexed, so verification touches a
// single row instead of every hash; only the digest of the whole string is
// stored, so a database leak yields nothing usable. API keys and invitations
// both have this shape, which is why it lives here rather than in either.
type Prefixed struct {
	// Full is shown once and never stored.
	Full string
	// Prefix is the indexed lookup component.
	Prefix string
	// Hash is what gets stored.
	Hash string
}

// Lengths of the two components. Fixed, so a presented token can be rejected on
// shape alone before any database work.
const (
	prefixLen = 12
	secretLen = 32
)

// NewPrefixed mints a token for a scheme.
func NewPrefixed(scheme string) Prefixed {
	prefix := ids.New(prefixLen)
	secret := ids.New(secretLen)
	full := scheme + "_" + prefix + "_" + secret
	return Prefixed{Full: full, Prefix: prefix, Hash: Hash(full)}
}

// SplitPrefixed extracts the lookup prefix from a presented token, reporting
// false for anything not shaped like one this scheme could have issued.
func SplitPrefixed(scheme, full string) (string, bool) {
	parts := strings.Split(full, "_")
	if len(parts) != 3 || parts[0] != scheme {
		return "", false
	}
	if len(parts[1]) != prefixLen || len(parts[2]) != secretLen {
		return "", false
	}
	return parts[1], true
}

// VerifyPrefixed reports whether presented matches the stored hash, in constant
// time.
func VerifyPrefixed(presented, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(Hash(presented)), []byte(storedHash)) == 1
}
