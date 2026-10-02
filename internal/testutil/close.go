// SPDX-License-Identifier: AGPL-3.0-or-later

// Package testutil holds small helpers shared by tests.
package testutil

import (
	"io"
	"testing"
)

// Close closes c and fails the test, without stopping it, if that fails. It
// suits a defer or a cleanup, where a test can no longer return an error.
func Close(t testing.TB, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}
