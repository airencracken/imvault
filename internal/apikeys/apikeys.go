// SPDX-License-Identifier: AGPL-3.0-or-later

// Package apikeys generates and verifies the bearer tokens used by the
// programmatic API.
//
// A key looks like "imv_<prefix>_<secret>". The prefix is stored in the clear
// and indexed, so a lookup touches a single row; only the SHA-256 of the whole
// key is stored, so a database leak does not yield usable credentials.
package apikeys

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"imvault/internal/ids"
)

// Scheme is the literal prefix every key starts with.
const Scheme = "imv"

// prefixLen is the number of characters in the public lookup prefix.
const prefixLen = 12

// secretLen is the number of characters in the secret portion.
const secretLen = 32

// Generated holds the three representations of a freshly minted key.
type Generated struct {
	// Full is shown to the user exactly once.
	Full string
	// Prefix is the indexed lookup component.
	Prefix string
	// Hash is what gets stored.
	Hash string
}

// Generate mints a new API key.
func Generate() Generated {
	prefix := ids.New(prefixLen)
	secret := ids.New(secretLen)
	return Generated{
		Full:   Scheme + "_" + prefix + "_" + secret,
		Prefix: prefix,
		Hash:   Hash(Scheme + "_" + prefix + "_" + secret),
	}
}

// Hash returns the storage hash for a full key.
func Hash(full string) string {
	sum := sha256.Sum256([]byte(full))
	return hex.EncodeToString(sum[:])
}

// Split extracts the lookup prefix from a presented key. It reports false for
// anything that is not shaped like a key we could have issued.
func Split(full string) (prefix string, ok bool) {
	parts := strings.Split(full, "_")
	if len(parts) != 3 {
		return "", false
	}
	if parts[0] != Scheme {
		return "", false
	}
	if len(parts[1]) != prefixLen || len(parts[2]) != secretLen {
		return "", false
	}
	return parts[1], true
}

// Verify reports whether presented matches the stored hash for a key, using a
// constant-time comparison.
func Verify(presented, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(Hash(presented)), []byte(storedHash)) == 1
}
