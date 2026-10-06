// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"imvault/internal/models"
)

func TestVersionBrandingFallbackAndAtomicity(t *testing.T) {
	s, _ := newTestStore(t)
	settings := models.Settings{AnonymousTTL: time.Hour, DefaultVisibility: models.VisibilityMembers}
	for _, show := range []bool{false, true, false, true} {
		if err := s.SaveSettingsWithBranding(t.Context(), settings, models.Branding{ShowVersion: show}); err != nil {
			t.Fatal(err)
		}
		brand, sources, err := s.LoadBranding(t.Context(), models.Branding{ShowVersion: !show})
		if err != nil || brand.ShowVersion != show || !sources[models.SettingShowVersion] {
			t.Fatal("round trip", brand, err)
		}
	}
	// A database failure must roll back the entire policy and branding write.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_version BEFORE UPDATE ON settings WHEN NEW.key='show_version' BEGIN SELECT RAISE(ABORT,'write failed'); END;`); err != nil {
		t.Fatal(err)
	}
	changed := settings
	changed.AllowSignup = true
	if err := s.SaveSettingsWithBranding(t.Context(), changed, models.Branding{ShowVersion: false}); err == nil {
		t.Fatal("write failure ignored")
	}
	brand, _, err := s.LoadBranding(t.Context(), models.Branding{})
	policy, _, policyErr := s.LoadSettings(t.Context(), models.Settings{})
	if err != nil || policyErr != nil || !brand.ShowVersion || policy.AllowSignup {
		t.Fatal("partial settings write", brand, policy, err, policyErr)
	}
	if _, err := s.db.Exec(`DELETE FROM settings WHERE key='show_version'`); err != nil {
		t.Fatal(err)
	}
	brand, _, err = s.LoadBranding(t.Context(), models.Branding{})
	if err != nil || brand.ShowVersion {
		t.Fatal("absent option not hidden", brand, err)
	}
	if _, err := s.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('show_version','<script>',0)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LoadBranding(t.Context(), models.Branding{}); err == nil {
		t.Fatal("corrupt boolean accepted")
	}
}
