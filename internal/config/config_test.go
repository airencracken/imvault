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

func TestConcurrentUploadsSelfTunesAndValidates(t *testing.T) {
	// Unset means "work it out": a one-core box and a sixteen-core one want
	// different answers, and the number only has to be low enough that the
	// simultaneous ffmpeg invocations fit.
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrentUploads < 2 {
		t.Errorf("default concurrency = %d, want at least 2", cfg.MaxConcurrentUploads)
	}

	t.Setenv("IMVAULT_MAX_CONCURRENT_UPLOADS", "3")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrentUploads != 3 {
		t.Errorf("concurrency = %d, want the configured 3", cfg.MaxConcurrentUploads)
	}

	t.Setenv("IMVAULT_MAX_CONCURRENT_UPLOADS", "-1")
	if _, err := Load(); err == nil {
		t.Error("a negative concurrency was accepted")
	}
}

func TestTheInstanceCeilingDefaultsToNone(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxTotalBytes != 0 {
		t.Errorf("default ceiling = %d, want none", cfg.MaxTotalBytes)
	}

	t.Setenv("IMVAULT_MAX_TOTAL_BYTES", "1073741824")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxTotalBytes != 1<<30 {
		t.Errorf("ceiling = %d, want 1 GiB", cfg.MaxTotalBytes)
	}

	t.Setenv("IMVAULT_MAX_TOTAL_BYTES", "-1")
	if _, err := Load(); err == nil {
		t.Error("a negative ceiling was accepted")
	}
}

func TestWhiteboxIdentityHasSourceLinkDefaultsAndEnvironmentOverrides(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	t.Setenv("IMVAULT_NAME", "")
	t.Setenv("IMVAULT_SOURCE_URL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "Imvault" || cfg.SourceURL != "https://github.com/airencracken/imvault" {
		t.Fatalf("identity defaults = %q / %q", cfg.Name, cfg.SourceURL)
	}

	t.Setenv("IMVAULT_NAME", "Friends' Album")
	t.Setenv("IMVAULT_SOURCE_URL", "https://example.org/source")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "Friends' Album" || cfg.SourceURL != "https://example.org/source" {
		t.Errorf("identity overrides = %q / %q", cfg.Name, cfg.SourceURL)
	}
}

func TestTheOptionalLocationMap(t *testing.T) {
	load := func(t *testing.T, env map[string]string) (*Config, error) {
		t.Helper()
		t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
		t.Setenv("IMVAULT_MAP_URL", "")
		t.Setenv("IMVAULT_MAP_KEY", "")
		t.Setenv("IMVAULT_MAP_ZOOM", "")
		for key, value := range env {
			t.Setenv(key, value)
		}
		return Load()
	}

	// Off unless a template is given, with a sensible zoom ready for the link.
	cfg, err := load(t, nil)
	if err != nil || cfg.MapURL != "" || cfg.MapZoom != 13 {
		t.Fatalf("default map config = %+v, %v", cfg, err)
	}

	// A keyless template is complete on its own.
	if _, err := load(t, map[string]string{"IMVAULT_MAP_URL": "https://maps.example/{lat},{lon}?z={zoom}"}); err != nil {
		t.Fatalf("keyless template rejected: %v", err)
	}

	// A template that wants a key without one is half a configuration.
	if _, err := load(t, map[string]string{"IMVAULT_MAP_URL": "https://maps.example/{lat}/{lon}?k={key}"}); err == nil {
		t.Fatal("a template wanting a key was accepted without one")
	}
	if _, err := load(t, map[string]string{
		"IMVAULT_MAP_URL": "https://maps.example/{lat}/{lon}?k={key}",
		"IMVAULT_MAP_KEY": "secret",
	}); err != nil {
		t.Fatalf("keyed template rejected: %v", err)
	}

	// Missing placeholders, bad schemes, and an out-of-range zoom are refused.
	for _, env := range []map[string]string{
		{"IMVAULT_MAP_URL": "https://maps.example/static.png"},
		{"IMVAULT_MAP_URL": "ftp://maps.example/{lat}/{lon}"},
		{"IMVAULT_MAP_URL": "https://maps.example/{lat}/{lon}", "IMVAULT_MAP_ZOOM": "99"},
	} {
		if _, err := load(t, env); err == nil {
			t.Errorf("%v was accepted", env)
		}
	}
}
