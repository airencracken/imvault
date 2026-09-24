// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"strings"
	"testing"
	"time"

	"imvault/internal/models"
)

func TestBrandingSettingsFallbackPersistAndClear(t *testing.T) {
	s, ctx := newTestStore(t)
	defaults := models.Branding{
		SiteName:     "imvault",
		SourceURL:    "https://github.com/airencracken/imvault",
		WelcomeTitle: "Your pictures, your server.",
		WelcomeText:  "Default welcome.",
	}
	resolved, stored, err := s.LoadBranding(ctx, defaults)
	if err != nil || resolved != defaults || len(stored) != 0 {
		t.Fatalf("default branding: %+v stored=%v err=%v", resolved, stored, err)
	}
	settings := models.Settings{
		AllowSignup:       true,
		AnonymousTTL:      24 * time.Hour,
		DefaultVisibility: models.VisibilityMembers,
	}
	custom := models.Branding{
		SiteName:     "Friends' Album",
		SourceURL:    "",
		WelcomeTitle: "A shared place.",
		WelcomeText:  "Welcome to our group.",
	}
	if err := s.SaveSettingsWithBranding(ctx, settings, custom); err != nil {
		t.Fatal(err)
	}
	resolved, stored, err = s.LoadBranding(ctx, defaults)
	if err != nil || resolved != custom || len(stored) != 4 {
		t.Fatalf("custom branding: %+v stored=%v err=%v", resolved, stored, err)
	}
	invalidPolicy := settings
	invalidPolicy.AnonymousTTL = 0
	invalid := custom
	invalid.SiteName = "Should roll back"
	if err := s.SaveSettingsWithBranding(ctx, invalidPolicy, invalid); err == nil {
		t.Fatal("invalid policy was saved with branding")
	}
	resolved, _, err = s.LoadBranding(ctx, defaults)
	if err != nil || resolved != custom {
		t.Fatalf("failed settings write changed branding: %+v err=%v", resolved, err)
	}
	if _, err := s.ClearSettings(ctx); err != nil {
		t.Fatal(err)
	}
	resolved, stored, err = s.LoadBranding(ctx, defaults)
	if err != nil || resolved != defaults || len(stored) != 0 {
		t.Fatalf("cleared branding: %+v stored=%v err=%v", resolved, stored, err)
	}
}

func TestBrandingValidationRejectsUnsafeSourceAndControls(t *testing.T) {
	for _, branding := range []models.Branding{
		{SourceURL: "javascript:alert(1)"},
		{SourceURL: "https://user@example.org/source"},
		{SourceURL: "https://example.org/" + strings.Repeat("x", 513)},
		{SiteName: "bad\nname"},
	} {
		if err := ValidateBranding(branding); err == nil {
			t.Errorf("accepted invalid branding: %+v", branding)
		}
	}
}
