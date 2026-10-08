// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"strings"
	"testing"
)

func TestSharedCSRFCompatibility(t *testing.T) {
	if got := sessionCSRFToken(strings.Repeat("a", 64)); got != "5e50f58e8ce9dbbdbb60d09f404f92dce2ac0cc6ef1b5162bf9fe76c76423f4c" {
		t.Fatal("existing session CSRF tokens changed", got)
	}
}
