// SPDX-License-Identifier: AGPL-3.0-or-later

// Package apikeys generates and verifies the bearer tokens used by the
// programmatic API.
//
// A key looks like "imv_<prefix>_<secret>". The prefix is stored in the clear
// and indexed, so a lookup touches a single row; only the SHA-256 of the whole
// key is stored, so a database leak does not yield usable credentials.
//
// The shape is shared with invitations, so the implementation lives in
// package tokens and this is the scheme that names it.
package apikeys

import "imvault/internal/tokens"

// Scheme is the literal prefix every key starts with.
const Scheme = "imv"

// Generated holds the three representations of a freshly minted key.
type Generated = tokens.Prefixed

// Generate mints a new API key.
func Generate() Generated { return tokens.NewPrefixed(Scheme) }

// Hash returns the storage hash for a full key.
func Hash(full string) string { return tokens.Hash(full) }

// Split extracts the lookup prefix from a presented key. It reports false for
// anything that is not shaped like a key we could have issued.
func Split(full string) (prefix string, ok bool) { return tokens.SplitPrefixed(Scheme, full) }

// Verify reports whether presented matches the stored hash for a key, using a
// constant-time comparison.
func Verify(presented, storedHash string) bool {
	return tokens.VerifyPrefixed(presented, storedHash)
}
