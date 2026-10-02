// SPDX-License-Identifier: AGPL-3.0-or-later

// Package closer releases read-only resources whose Close cannot report
// anything the caller has not already seen.
//
// Closing database rows after iterating them, or a file that was only read,
// does not lose data: a failure while reading surfaces through the read itself
// or through Rows.Err. Writers are different, and are always closed with their
// error checked, so this package is deliberately only for the read side.
package closer

import "io"

// Discard closes c and drops the error, for use in a defer on a resource that
// was only read from.
func Discard(c io.Closer) {
	_ = c.Close() // nothing was written, so there is nothing to lose
}
