// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"imvault/internal/config"
)

func TestClientIPBelievesOnlyConfiguredProxies(t *testing.T) {
	private := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, tc := range []struct {
		name     string
		trusted  []netip.Prefix
		peer     string
		forwards []string
		realIP   string
		want     string
	}{
		{"no proxies configured", nil, "10.0.0.1:5555", []string{"203.0.113.9"}, "", "10.0.0.1"},
		{"loopback is not trusted by default", nil, "127.0.0.1:5555", []string{"203.0.113.9"}, "", "127.0.0.1"},
		{"single", private, "10.0.0.1:5555", []string{"203.0.113.9"}, "", "203.0.113.9"},
		{"client prepended a guess", private, "10.0.0.1:5555", []string{"198.51.100.7, 203.0.113.9"}, "", "203.0.113.9"},
		{"repeated header", private, "10.0.0.1:5555", []string{"198.51.100.7", "192.0.2.4, 203.0.113.9"}, "", "203.0.113.9"},
		{"spaces", private, "10.0.0.1:5555", []string{" 198.51.100.7 ,  203.0.113.9 "}, "", "203.0.113.9"},
		{"chained trusted proxies", private, "10.0.0.1:5555", []string{"203.0.113.9, 10.1.2.3"}, "", "203.0.113.9"},
		{"untrusted peer", private, "192.0.2.50:5555", []string{"203.0.113.9"}, "", "192.0.2.50"},
		{"trailing empty entry", private, "10.0.0.1:5555", []string{"203.0.113.9,"}, "", "10.0.0.1"},
		{"hop with a port", private, "10.0.0.1:5555", []string{"203.0.113.9:443"}, "", "10.0.0.1"},
		{"hop that is a name", private, "10.0.0.1:5555", []string{"attacker.example"}, "", "10.0.0.1"},
		{"X-Real-IP is never read", private, "10.0.0.1:5555", nil, "192.0.2.1", "10.0.0.1"},
		{"X-Real-IP beside a forward", private, "10.0.0.1:5555", []string{"203.0.113.9"}, "192.0.2.1", "203.0.113.9"},
		{"mapped peer", private, "[::ffff:10.0.0.1]:5555", []string{"203.0.113.9"}, "", "203.0.113.9"},
		{"nothing forwarded", private, "10.0.0.1:5555", nil, "", "10.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessWith(t, func(c *config.Config) { c.TrustedProxies = tc.trusted })
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.peer
			for _, value := range tc.forwards {
				req.Header.Add("X-Forwarded-For", value)
			}
			if tc.realIP != "" {
				req.Header.Set("X-Real-IP", tc.realIP)
			}
			if got := h.srv.clientIP(req).String(); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// One host owns a whole IPv6 /64, so every address in it shares one budget;
// otherwise each request could come from a fresh address with a fresh budget.
func TestAnonymousRateLimitKeysGroupIPv6ByNetwork(t *testing.T) {
	h := newHarness(t)
	key := func(peer string) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = peer
		return h.srv.rateLimitKey(req)
	}
	same := [][2]string{
		{"[2001:db8:1:2::1]:1000", "[2001:db8:1:2:ffff:ffff:ffff:ffff]:2000"},
		{"[fe80::1%eth0]:1000", "[fe80::2]:1000"},
		{"[::ffff:192.0.2.1]:1000", "192.0.2.1:2000"},
	}
	for _, pair := range same {
		if key(pair[0]) != key(pair[1]) {
			t.Errorf("%s and %s have different budgets: %q %q", pair[0], pair[1], key(pair[0]), key(pair[1]))
		}
	}
	different := [][2]string{
		{"[2001:db8:1:2::1]:1000", "[2001:db8:1:3::1]:1000"},
		{"192.0.2.1:1000", "192.0.2.2:1000"},
		{"garbage", "192.0.2.1:1000"},
	}
	for _, pair := range different {
		if key(pair[0]) == key(pair[1]) {
			t.Errorf("%s and %s share a budget: %q", pair[0], pair[1], key(pair[0]))
		}
	}
	if got := key("[2001:db8:1:2::1]:1000"); got != "ip:2001:db8:1:2::/64" {
		t.Errorf("IPv6 key = %q", got)
	}
}

// End to end through the sign-in limiter: addresses from one /64 spend one
// budget, and another network keeps its own.
func TestSignInBudgetIsSharedAcrossAnIPv6Network(t *testing.T) {
	h := newHarnessWith(t, func(c *config.Config) {
		c.LoginRatePerHour = 1
		c.LoginBurst = 2
	})
	handler := h.srv.rateLimitLogins(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	status := func(peer string) int {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = peer
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Code
	}
	for i, peer := range []string{"[2001:db8:5::1]:1", "[2001:db8:5::2]:1"} {
		if got := status(peer); got != http.StatusNoContent {
			t.Fatalf("attempt %d from %s = %d, want it allowed", i+1, peer, got)
		}
	}
	if got := status("[2001:db8:5::3]:1"); got != http.StatusTooManyRequests {
		t.Fatalf("a third address in the same /64 = %d, want 429", got)
	}
	if got := status("[2001:db8:6::1]:1"); got != http.StatusNoContent {
		t.Fatalf("another /64 = %d, want its own budget", got)
	}
}
