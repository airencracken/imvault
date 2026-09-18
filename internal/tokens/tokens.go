// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tokens mints opaque secrets and hashes them for storage.
//
// Sessions, API keys and auth tokens all follow the same shape: a high-entropy
// string handed to the client, of which only a digest is kept. Keeping that in
// one place means there is one implementation to get right.
package tokens

import (
	"crypto/sha256"
	"encoding/hex"

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
