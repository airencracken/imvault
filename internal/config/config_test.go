// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"imvault/internal/models"
)

func TestDefaultVisibilityDefaultsToMembers(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// A fresh instance is a group instance: middle of the road, not closed and
	// not open to the world.
	if cfg.DefaultVisibility != models.VisibilityMembers {
		t.Errorf("default visibility = %q, want members", cfg.DefaultVisibility)
	}
}

func TestDefaultVisibilityIsReadAndNormalised(t *testing.T) {
	for raw, want := range map[string]models.Visibility{
		"public":   models.VisibilityPublic,
		"PUBLIC":   models.VisibilityPublic,
		" Members": models.VisibilityMembers,
		"private ": models.VisibilityPrivate,
	} {
		t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
		t.Setenv("IMVAULT_DEFAULT_VISIBILITY", raw)

		cfg, err := Load()
		if err != nil {
			t.Errorf("IMVAULT_DEFAULT_VISIBILITY=%q: %v", raw, err)
			continue
		}
		if cfg.DefaultVisibility != want {
			t.Errorf("IMVAULT_DEFAULT_VISIBILITY=%q gave %q, want %q", raw, cfg.DefaultVisibility, want)
		}
	}
}

func TestDefaultVisibilityRejectsATypo(t *testing.T) {
	// The whole point of validating rather than parsing leniently: a typo must
	// stop the server, not silently pick a level for it.
	for _, raw := range []string{"pubic", "member", "true", "no"} {
		t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
		t.Setenv("IMVAULT_DEFAULT_VISIBILITY", raw)

		if _, err := Load(); err == nil {
			t.Errorf("IMVAULT_DEFAULT_VISIBILITY=%q was accepted", raw)
		}
	}
}

func TestEmptyDefaultVisibilityMeansUnset(t *testing.T) {
	// An empty variable is unset everywhere else in this file, so it is unset
	// here too rather than a value to validate.
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	t.Setenv("IMVAULT_DEFAULT_VISIBILITY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DefaultVisibility != models.VisibilityMembers {
		t.Errorf("default visibility = %q, want members", cfg.DefaultVisibility)
	}
}

func TestOIDCNeedsBothHalvesOrNeither(t *testing.T) {
	// Half a provider would put a button on the sign-in page that fails at the
	// provider, which is worse than no button at all.
	for name, env := range map[string]map[string]string{
		"an issuer with no client id": {"IMVAULT_OIDC_ISSUER": "https://id.example"},
		"a client id with no issuer":  {"IMVAULT_OIDC_CLIENT_ID": "imvault"},
	} {
		t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
		for key, value := range env {
			t.Setenv(key, value)
		}
		if _, err := Load(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestOIDCNeedsAStableAddress(t *testing.T) {
	// The redirect URI is registered with the provider and has to match
	// exactly, so it cannot be derived from a request's Host header.
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	t.Setenv("IMVAULT_OIDC_ISSUER", "https://id.example")
	t.Setenv("IMVAULT_OIDC_CLIENT_ID", "imvault")

	if _, err := Load(); err == nil {
		t.Fatal("a provider was configured without a base URL")
	}

	t.Setenv("IMVAULT_BASE_URL", "https://img.example")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.OIDCIssuer != "https://id.example" || cfg.OIDCClientID != "imvault" {
		t.Errorf("provider = %q / %q", cfg.OIDCIssuer, cfg.OIDCClientID)
	}
	// A trailing slash on the issuer would make discovery ask for a doubled
	// path, so it is trimmed.
	if strings.HasSuffix(cfg.OIDCIssuer, "/") {
		t.Errorf("issuer = %q, want no trailing slash", cfg.OIDCIssuer)
	}
}

func TestOIDCListsAreSplitAndTrimmed(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	t.Setenv("IMVAULT_OIDC_SCOPES", "openid, profile  email,")
	t.Setenv("IMVAULT_OIDC_ALLOWED_DOMAINS", "@one.example, two.example")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if got := cfg.OIDCScopes; len(got) != 3 || got[0] != "openid" || got[2] != "email" {
		t.Errorf("scopes = %#v, want three entries with no empties", got)
	}
	if got := cfg.OIDCAllowedDomains; len(got) != 2 || got[1] != "two.example" {
		t.Errorf("allowed domains = %#v", got)
	}
}
