// SPDX-License-Identifier: AGPL-3.0-or-later

package oidc

import (
	"testing"
	"time"
)

func TestConfigIsOffUnlessItIsWhole(t *testing.T) {
	cases := map[string]Config{
		"nothing":                    {},
		"an issuer alone":            {Issuer: "https://id.example"},
		"a client id alone":          {ClientID: "imvault"},
		"whitespace for an issuer":   {Issuer: "   ", ClientID: "imvault"},
		"whitespace for a client id": {Issuer: "https://id.example", ClientID: " "},
	}
	for name, cfg := range cases {
		if cfg.Configured() {
			t.Errorf("%s counts as configured", name)
		}
	}

	whole := Config{Issuer: "https://id.example", ClientID: "imvault"}
	if !whole.Configured() {
		t.Error("a whole configuration does not count as configured")
	}
	if got := whole.DisplayName(); got != "Single sign-on" {
		t.Errorf("DisplayName = %q, want a fallback", got)
	}
	if got := (Config{Name: "  Example  "}).DisplayName(); got != "Example" {
		t.Errorf("DisplayName = %q, want the configured name, trimmed", got)
	}
}

func TestLazyIsOffWithoutAProvider(t *testing.T) {
	// A nil Lazy is reachable: the server always holds one, but a test harness
	// or a future caller might not.
	var none *Lazy
	if none.Enabled() {
		t.Error("a nil provider reports itself as enabled")
	}
	if got := none.Name(); got != "" {
		t.Errorf("a nil provider has the name %q", got)
	}

	// The important one: an unconfigured provider must not fall back to a
	// display name, because templates key the button off it and would then
	// offer a sign-in that cannot work.
	off := NewLazy(Config{})
	if off.Enabled() {
		t.Error("an empty configuration reports itself as enabled")
	}
	if got := off.Name(); got != "" {
		t.Errorf("an unconfigured provider is named %q, so a button would appear", got)
	}

	on := NewLazy(Config{Issuer: "https://id.example", ClientID: "imvault"})
	if !on.Enabled() {
		t.Error("a configured provider reports itself as disabled")
	}
	if got := on.Name(); got != "Single sign-on" {
		t.Errorf("Name = %q", got)
	}
}

func TestAttemptExpires(t *testing.T) {
	now := time.Now()
	attempt := Attempt{CreatedAt: now.Add(-time.Minute)}
	window := 10 * time.Minute

	if attempt.Expired(now, window) {
		t.Error("a minute-old attempt is not expired")
	}
	if !attempt.Expired(now.Add(20*time.Minute), window) {
		t.Error("a twenty-minute-old attempt is not expired")
	}
}

func TestEmailDomain(t *testing.T) {
	for raw, want := range map[string]string{
		"person@example.com":    "example.com",
		"a.b@sub.example.co.uk": "sub.example.co.uk",
		"no-at-sign":            "",
		"trailing@":             "",
		"@leading.example":      "leading.example",
	} {
		if got := emailDomain(raw); got != want {
			t.Errorf("emailDomain(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestAllowedDomains(t *testing.T) {
	provider := &Provider{cfg: Config{AllowedDomains: []string{"@one.example", "Two.Example"}}}

	// An allowlist is only consulted when one is set, and comparison is
	// case-insensitive because a provider may return either.
	for _, email := range []string{"a@one.example", "b@TWO.example", "c@two.example"} {
		if err := provider.allows(&Identity{Email: email}); err != nil {
			t.Errorf("%s was refused: %v", email, err)
		}
	}
	for _, email := range []string{"a@three.example", "", "no-at-sign"} {
		if err := provider.allows(&Identity{Email: email}); err == nil {
			t.Errorf("%q was allowed", email)
		}
	}

	// With no allowlist, any address is acceptable.
	open := &Provider{}
	if err := open.allows(&Identity{Email: "anyone@anywhere"}); err != nil {
		t.Errorf("an unset allowlist refused somebody: %v", err)
	}
}
