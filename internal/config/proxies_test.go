// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func prefixes(values ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, value := range values {
		out = append(out, netip.MustParsePrefix(value))
	}
	return out
}

// loadProxies loads the configuration with IMVAULT_TRUSTED_PROXIES set to
// trusted; an empty string leaves it unset. The retired setting is unset.
func loadProxies(t *testing.T, trusted string) (*Config, error) {
	t.Helper()
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	t.Setenv("IMVAULT_TRUSTED_PROXIES", trusted)
	return Load()
}

func TestTrustedProxiesMatrix(t *testing.T) {
	loopback := prefixes("127.0.0.1/32", "::1/128")
	for _, tc := range []struct {
		name, trusted string
		want          []netip.Prefix
	}{
		{name: "unset trusts nothing", want: nil},
		{name: "blank trusts nothing", trusted: "  ", want: nil},
		{name: "loopback", trusted: "127.0.0.1/32,::1/128", want: loopback},
		{name: "bare addresses", trusted: "127.0.0.1, ::1", want: loopback},
		{name: "network is masked", trusted: "10.1.2.3/8", want: prefixes("10.0.0.0/8")},
		{name: "mapped IPv4 is unmapped", trusted: "::ffff:192.0.2.0/120", want: prefixes("192.0.2.0/24")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadProxies(t, tc.trusted)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(cfg.TrustedProxies, tc.want) {
				t.Errorf("TrustedProxies = %v, want %v", cfg.TrustedProxies, tc.want)
			}
		})
	}
}

// The retired setting believed forwarding headers from any peer. It is
// refused whatever its value, alone or beside its replacement, with a message
// naming the replacement, rather than being given a meaning of its own.
func TestTheReplacedProxySettingStopsStartup(t *testing.T) {
	for _, legacy := range []string{"true", "false", "", "1", "maybe"} {
		for _, trusted := range []string{"", "127.0.0.1/32,::1/128"} {
			t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
			t.Setenv("IMVAULT_TRUSTED_PROXIES", trusted)
			t.Setenv("IMVAULT_TRUST_PROXY_HEADERS", legacy)
			cfg, err := Load()
			if err == nil {
				t.Fatalf("IMVAULT_TRUST_PROXY_HEADERS=%q with IMVAULT_TRUSTED_PROXIES=%q started, trusting %v", legacy, trusted, cfg.TrustedProxies)
			}
			for _, want := range []string{"IMVAULT_TRUST_PROXY_HEADERS", "replaced by IMVAULT_TRUSTED_PROXIES", "IMVAULT_TRUSTED_PROXIES=127.0.0.1/32,::1/128"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("IMVAULT_TRUST_PROXY_HEADERS=%q: the error does not say %q: %v", legacy, want, err)
				}
			}
		}
	}
}

func TestInvalidTrustedProxiesStopStartup(t *testing.T) {
	for _, raw := range []string{
		"localhost",
		"127.0.0.1/33",
		"::1/129",
		"127.0.0.1/32,",
		" , ",
		",127.0.0.1",
		"127.0.0.1 ::1",
		"fe80::1%eth0",
		"::ffff:10.0.0.0/8",
		"10.0.0.0/8\n192.0.2.0/24",
		"*",
		"0.0.0.0/-1",
		"1.2.3.4:80",
	} {
		_, err := loadProxies(t, raw)
		if err == nil || !strings.Contains(err.Error(), "IMVAULT_TRUSTED_PROXIES") {
			t.Errorf("IMVAULT_TRUSTED_PROXIES=%q: %v", raw, err)
		}
	}
}

// Property: any list of valid prefixes round-trips, in order, masked.
func TestTrustedProxiesRoundTrip(t *testing.T) {
	for bits := 0; bits <= 32; bits += 4 {
		for _, base := range []string{"192.0.2.77", "10.20.30.40", "255.255.255.255"} {
			v4 := netip.PrefixFrom(netip.MustParseAddr(base), bits)
			v6 := netip.PrefixFrom(netip.MustParseAddr("2001:db8:abcd:1234::99"), bits*4)
			raw := fmt.Sprintf("%s , %s", v4, v6)
			cfg, err := loadProxies(t, raw)
			if err != nil {
				t.Fatalf("%q: %v", raw, err)
			}
			if want := []netip.Prefix{v4.Masked(), v6.Masked()}; !slices.Equal(cfg.TrustedProxies, want) {
				t.Fatalf("%q gave %v, want %v", raw, cfg.TrustedProxies, want)
			}
		}
	}
}
