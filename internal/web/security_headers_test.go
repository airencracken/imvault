// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"testing"
)

// Every response, pages and uploaded files alike, carries the headers that
// stop content sniffing and framing.
func TestEveryResponseCarriesSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	_, body := h.upload("a.png")
	id := firstFileID(t, body)

	for _, path := range []string{"/", "/login", "/f/" + id, "/f/" + id + "/raw", "/f/" + id + "/thumb",
		"/static/js/app.js", "/missing", "/api/v1/me"} {
		resp, _ := h.get(path)
		for header, want := range map[string]string{
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Content-Security-Policy": "frame-ancestors 'none'",
			"Referrer-Policy":         "same-origin",
		} {
			if got := resp.Header.Get(header); got != want {
				t.Errorf("%s %s = %q, want %q (status %d)", path, header, got, want, resp.StatusCode)
			}
		}
	}
	if resp, _ := h.get("/healthz"); resp.StatusCode != http.StatusOK || resp.Header.Get("X-Frame-Options") == "" {
		t.Error("the health check lacks the headers")
	}
}
