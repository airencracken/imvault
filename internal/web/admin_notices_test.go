// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestAdminPagesOnlyRenderActualNoticesOnce(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	for _, path := range []string{"/admin", "/admin/users", "/admin/mail", "/admin/settings"} {
		t.Run(path, func(t *testing.T) {
			resp, page := h.get(path)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET = %d, want 200", resp.StatusCode)
			}
			if strings.Contains(page, "{ false") {
				t.Error("page without a notice shows an empty or serialized flash message")
			}
			for _, kind := range []flashKind{flashNotice, flashError} {
				_, page := h.get(h.srv.flashURL(path, kind, "saved-settings-test"))
				if count := strings.Count(page, "saved-settings-test"); count != 1 {
					t.Errorf("%s appears %d times, want once", kind, count)
				}
			}
		})
	}
}
