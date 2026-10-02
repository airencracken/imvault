// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http/httptest"
	"testing"

	"imvault/internal/config"
)

func TestClientIPTakesTheProxysOwnEntry(t *testing.T) {
	trusting := newHarnessWith(t, func(c *config.Config) { c.TrustProxyHeaders = true })
	ignoring := newHarness(t)

	for _, tc := range []struct {
		name     string
		forwards []string
		realIP   string
		trusted  string
	}{
		{"single", []string{"203.0.113.9"}, "", "203.0.113.9"},
		{"client prepended a guess", []string{"198.51.100.7, 203.0.113.9"}, "", "203.0.113.9"},
		{"repeated header", []string{"198.51.100.7", "192.0.2.4, 203.0.113.9"}, "", "203.0.113.9"},
		{"spaces", []string{" 198.51.100.7 ,  203.0.113.9 "}, "", "203.0.113.9"},
		{"trailing empty entry", []string{"203.0.113.9,"}, "192.0.2.1", "192.0.2.1"},
		{"real ip only", nil, "192.0.2.1", "192.0.2.1"},
		{"nothing", nil, "", "10.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "10.0.0.1:5555"
			for _, value := range tc.forwards {
				req.Header.Add("X-Forwarded-For", value)
			}
			if tc.realIP != "" {
				req.Header.Set("X-Real-IP", tc.realIP)
			}
			if got := trusting.srv.clientIP(req); got != tc.trusted {
				t.Errorf("trusted proxy: %q, want %q", got, tc.trusted)
			}
			if got := ignoring.srv.clientIP(req); got != "10.0.0.1" {
				t.Errorf("untrusted headers were read: %q", got)
			}
		})
	}
}
