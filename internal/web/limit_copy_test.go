// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"strings"
	"testing"

	"imvault/internal/config"
)

// The upload limits stay binary, and the copy says so in MiB.
func TestUploadLimitCopyMatchesTheBinaryLimit(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.MaxUploadBytes = 5 << 20
		cfg.MaxVideoBytes = 128 << 20
	})
	alice := h.seedUser("alice")
	_, page := h.sessionFor(t, alice.ID).get("/upload")
	if !strings.Contains(page, "up to 5 MiB") {
		t.Errorf("the image limit of %d bytes is not shown as 5 MiB", 5<<20)
	}
	if h.srv.media.VideoEnabled() && !strings.Contains(page, "128 MiB") {
		t.Errorf("the clip limit is not shown as 128 MiB")
	}
	for _, decimal := range []string{" MB", " kB", " GB", "5.2 M"} {
		if strings.Contains(page, decimal) {
			t.Errorf("the upload page uses a decimal size: %q", decimal)
		}
	}
	// Avatars and branding images promise 2 MiB in their copy.
	if maxBrandImageBytes != 2<<20 {
		t.Errorf("the image limit is %d bytes, but the copy promises 2 MiB", maxBrandImageBytes)
	}
}
