// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import "testing"

func TestMediaSandboxConfiguration(t *testing.T) {
	for _, value := range []string{"false", "true"} {
		t.Setenv("IMVAULT_MEDIA_SANDBOX", value)
		t.Setenv("IMVAULT_BWRAP", "/usr/bin/bwrap")
		cfg, err := Load()
		if err != nil || cfg.MediaSandbox != (value == "true") || cfg.BwrapPath != "/usr/bin/bwrap" {
			t.Fatalf("configuration %s: %+v %v", value, cfg, err)
		}
	}
	for _, value := range []string{"1", "TRUE", "auto", "enabled", " true ", "true\n"} {
		t.Setenv("IMVAULT_MEDIA_SANDBOX", value)
		if _, err := Load(); err == nil {
			t.Fatalf("accepted invalid sandbox mode %q", value)
		}
	}
}
