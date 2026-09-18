// SPDX-License-Identifier: AGPL-3.0-or-later

// Package invites mints the codes that admit an account to an instance whose
// registration is closed or restricted.
//
// An invitation is shaped like an API key — "inv_<prefix>_<secret>" — and for
// the same reason: the prefix is stored in the clear so redemption touches one
// row, and only a digest of the whole code is kept, so a database leak does not
// hand anybody an account.
//
// A code is shown once, when it is created. An administrator who loses one
// makes another and revokes this, which is the same trade the API makes.
package invites

import "imvault/internal/tokens"

// Scheme is the literal prefix every invitation starts with.
const Scheme = "inv"

// Generated holds the three representations of a freshly minted code.
type Generated = tokens.Prefixed

// Generate mints a new invitation code.
func Generate() Generated { return tokens.NewPrefixed(Scheme) }

// Split extracts the lookup prefix from a presented code. It reports false for
// anything that is not shaped like a code we could have issued, so a rubbish
// value is rejected without touching the database.
func Split(full string) (prefix string, ok bool) { return tokens.SplitPrefixed(Scheme, full) }

// Verify reports whether a presented code matches a stored hash, in constant
// time.
func Verify(presented, storedHash string) bool {
	return tokens.VerifyPrefixed(presented, storedHash)
}
