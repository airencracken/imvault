// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"github.com/airencracken/comfylib/smtp"
)

func TestSMTPTLSModes(t *testing.T) {
	for raw, want := range map[string]smtp.TLSMode{
		"":         smtp.TLSStartTLS, // unset
		"starttls": smtp.TLSStartTLS,
		"implicit": smtp.TLSImplicit,
		"none":     smtp.TLSNone,
	} {
		t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
		t.Setenv("IMVAULT_SMTP_TLS", raw)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("IMVAULT_SMTP_TLS=%q: %v", raw, err)
		}
		if cfg.SMTPTLS != want {
			t.Errorf("IMVAULT_SMTP_TLS=%q gave %q, want %q", raw, cfg.SMTPTLS, want)
		}
	}
}

// An unknown mode used to fall through to a plain-text connection. Every
// spelling that is not exactly one of the three modes must stop the server,
// whether or not a relay is configured yet.
func TestUnknownSMTPTLSModesAreRefused(t *testing.T) {
	for _, raw := range []string{"tls", "ssl", "STARTTLS", "StartTLS", " starttls", "starttls ", "true", "false", "plain", "none\n", "starttls,none"} {
		for _, host := range []string{"", "smtp.example.com"} {
			t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
			t.Setenv("IMVAULT_BASE_URL", "https://img.example.com")
			t.Setenv("IMVAULT_SMTP_HOST", host)
			t.Setenv("IMVAULT_SMTP_TLS", raw)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "IMVAULT_SMTP_TLS") {
				t.Errorf("IMVAULT_SMTP_TLS=%q with host %q: %v", raw, host, err)
			}
		}
	}
}

func TestSMTPFromIsCheckedAndCanonicalAtStartup(t *testing.T) {
	for raw, want := range map[string]string{
		"":                                 "\"Imvault\" <no-reply@localhost>", // unset
		"no-reply@img.example.com":         "<no-reply@img.example.com>",
		"Imvault <no-reply@example.com>":   "\"Imvault\" <no-reply@example.com>",
		"Café Photos <photos@example.com>": "=?utf-8?q?Caf=C3=A9_Photos?= <photos@example.com>",
	} {
		t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
		t.Setenv("IMVAULT_SMTP_FROM", raw)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("IMVAULT_SMTP_FROM=%q: %v", raw, err)
		}
		if cfg.SMTPFrom != want {
			t.Errorf("IMVAULT_SMTP_FROM=%q gave %q, want %q", raw, cfg.SMTPFrom, want)
		}
	}
}

func TestMalformedSMTPFromIsRefused(t *testing.T) {
	for _, raw := range []string{
		"Imvault",
		"Imvault no-reply@example.com",
		"<>",
		"no-reply@example.com\r\nBcc: victim@example.org",
		"no-reply@example.com\nBcc: victim@example.org",
		"a@example.com, b@example.com",
		"Imvault <no-reply@example.com",
		"\xff\xfe@example.com",
	} {
		t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
		t.Setenv("IMVAULT_SMTP_FROM", raw)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "IMVAULT_SMTP_FROM") {
			t.Errorf("IMVAULT_SMTP_FROM=%q was accepted or unnamed: %v", raw, err)
		}
	}
}
