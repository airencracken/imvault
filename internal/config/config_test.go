// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
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
