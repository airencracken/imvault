// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A value that does not parse must stop the server with the variable's name,
// never quietly become the default. Several of these defaults are the open
// setting, so falling back would fail open.
func TestMalformedSettingsAreErrorsNotDefaults(t *testing.T) {
	cases := map[string]string{
		"IMVAULT_ALLOW_SIGNUP":            "no",
		"IMVAULT_ALLOW_ANONYMOUS_UPLOADS": "off",
		"IMVAULT_SECURE_COOKIES":          "yes",
		"IMVAULT_INVITE_ONLY":             "enabled",
		"IMVAULT_MAX_UPLOAD_BYTES":        "32MB",
		"IMVAULT_DEFAULT_QUOTA_BYTES":     "5 GiB",
		"IMVAULT_MAX_CONCURRENT_UPLOADS":  "four",
		"IMVAULT_UPLOAD_RATE_PER_HOUR":    "NaN",
		"IMVAULT_LOGIN_RATE_PER_HOUR":     "lots",
		"IMVAULT_SESSION_TTL":             "30 days",
		"IMVAULT_ANONYMOUS_TTL":           "1d",
		"IMVAULT_MAIL_RETRY_INTERVAL":     "99999999999999999999",
		"IMVAULT_SMTP_PORT":               "587/tcp",
		"IMVAULT_JPEG_QUALITY":            "high",
		"IMVAULT_MEDIA_SANDBOX":           "yes",
	}
	for key, raw := range cases {
		t.Run(key, func(t *testing.T) {
			t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
			t.Setenv(key, raw)
			_, err := Load()
			if err == nil {
				t.Fatalf("%s=%q was accepted", key, raw)
			}
			if !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), raw) {
				t.Fatalf("the error does not name the setting and value: %v", err)
			}
		})
	}
}

func TestEveryMalformedSettingIsReportedAtOnce(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	t.Setenv("IMVAULT_ALLOW_SIGNUP", "no")
	t.Setenv("IMVAULT_SECURE_COOKIES", "yes")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "IMVAULT_ALLOW_SIGNUP") || !strings.Contains(err.Error(), "IMVAULT_SECURE_COOKIES") {
		t.Fatalf("not every malformed setting was reported: %v", err)
	}
}

func TestWellFormedSettingsStillParse(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	t.Setenv("IMVAULT_ALLOW_SIGNUP", "FALSE")
	t.Setenv("IMVAULT_SECURE_COOKIES", "1")
	t.Setenv("IMVAULT_SESSION_TTL", "3600")
	t.Setenv("IMVAULT_ANONYMOUS_TTL", "2h")
	t.Setenv("IMVAULT_UPLOAD_RATE_PER_HOUR", "0.5")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AllowSignup || !cfg.SecureCookies || cfg.SessionTTL != time.Hour || cfg.AnonymousTTL != 2*time.Hour || cfg.UploadRatePerHour != 0.5 {
		t.Fatalf("well-formed settings misread: %+v", cfg)
	}
}

func TestNonPositiveIntervalsAreRefused(t *testing.T) {
	for _, key := range []string{"IMVAULT_SESSION_TTL", "IMVAULT_CLEANUP_INTERVAL"} {
		for _, raw := range []string{"0", "-1h"} {
			t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
			t.Setenv(key, raw)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("%s=%s accepted: %v", key, raw, err)
			}
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestMailNeedsAStableAddress(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	t.Setenv("IMVAULT_SMTP_HOST", "smtp.example.com")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "IMVAULT_BASE_URL") {
		t.Fatalf("mail without a base URL accepted: %v", err)
	}
	t.Setenv("IMVAULT_BASE_URL", "https://img.example.com")
	if _, err := Load(); err != nil {
		t.Fatalf("mail with a base URL refused: %v", err)
	}
	t.Setenv("IMVAULT_SMTP_PORT", "70000")
	if _, err := Load(); err == nil {
		t.Fatal("an impossible SMTP port was accepted")
	}
}

func TestBlankToolPathsTurnClipToolingOff(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	t.Setenv("IMVAULT_FFMPEG", "")
	t.Setenv("IMVAULT_FFPROBE", "")
	cfg, err := Load()
	if err != nil || cfg.FFmpegPath != "" || cfg.FFprobePath != "" {
		t.Fatalf("blank tool paths were replaced: %q %q %v", cfg.FFmpegPath, cfg.FFprobePath, err)
	}
}

func TestTheIssuerIsKeptExactlyAsConfigured(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	t.Setenv("IMVAULT_BASE_URL", "https://img.example.com")
	t.Setenv("IMVAULT_OIDC_CLIENT_ID", "imvault")
	t.Setenv("IMVAULT_OIDC_ISSUER", "https://tenant.example.auth0.com/")
	cfg, err := Load()
	if err != nil || cfg.OIDCIssuer != "https://tenant.example.auth0.com/" {
		t.Fatalf("issuer altered: %q %v", cfg.OIDCIssuer, err)
	}
}
