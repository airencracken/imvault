// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"imvault/internal/models"
)

// LoadSettings resolves the instance policy from stored values, falling back to
// the supplied defaults for anything an administrator has not set.
//
// The second return value lists the keys that are explicitly stored, so the
// admin page can say where each value is coming from rather than leaving an
// operator to guess whether the environment or the interface is in charge.
func (s *Store) LoadSettings(ctx context.Context, defaults models.Settings) (models.Settings, map[string]bool, error) {
	stored := map[string]string{}

	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return defaults, nil, fmt.Errorf("load settings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return defaults, nil, fmt.Errorf("scan setting: %w", err)
		}
		stored[key] = value
	}
	if err := rows.Err(); err != nil {
		return defaults, nil, fmt.Errorf("iterate settings: %w", err)
	}

	resolved := defaults
	sources := map[string]bool{}

	for key, target := range map[string]*bool{
		models.SettingAllowSignup:           &resolved.AllowSignup,
		models.SettingAllowAnonymousUploads: &resolved.AllowAnonymousUploads,
		models.SettingInviteOnly:            &resolved.InviteOnly,
	} {
		if parsed, err := strconv.ParseBool(stored[key]); err == nil {
			*target = parsed
			sources[key] = true
		}
	}
	if raw, ok := stored[models.SettingAnonymousTTLSeconds]; ok {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
			resolved.AnonymousTTL = time.Duration(seconds) * time.Second
			sources[models.SettingAnonymousTTLSeconds] = true
		}
	}
	if raw, ok := stored[models.SettingMaxTotalBytes]; ok {
		if bytes, err := strconv.ParseInt(raw, 10, 64); err == nil && bytes >= 0 {
			resolved.MaxTotalBytes = bytes
			sources[models.SettingMaxTotalBytes] = true
		}
	}
	if raw, ok := stored[models.SettingDefaultVisibility]; ok {
		if visibility := models.ParseVisibility(raw); visibility.Valid() {
			resolved.DefaultVisibility = visibility
			sources[models.SettingDefaultVisibility] = true
		}
	}

	return resolved, sources, nil
}

// LoadBranding resolves public-facing identity from stored overrides and the
// supplied configuration defaults.
func (s *Store) LoadBranding(ctx context.Context, defaults models.Branding) (models.Branding, map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN (?, ?, ?, ?)`,
		models.SettingSiteName, models.SettingSourceURL, models.SettingWelcomeTitle, models.SettingWelcomeText)
	if err != nil {
		return defaults, nil, fmt.Errorf("load branding: %w", err)
	}
	defer rows.Close()
	values := map[string]string{}
	stored := map[string]bool{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return defaults, nil, err
		}
		values[key] = value
		stored[key] = true
	}
	if err := rows.Err(); err != nil {
		return defaults, nil, err
	}
	resolved := defaults
	if stored[models.SettingSiteName] && strings.TrimSpace(values[models.SettingSiteName]) != "" {
		resolved.SiteName = values[models.SettingSiteName]
	}
	if stored[models.SettingSourceURL] {
		resolved.SourceURL = values[models.SettingSourceURL]
	}
	if stored[models.SettingWelcomeTitle] && strings.TrimSpace(values[models.SettingWelcomeTitle]) != "" {
		resolved.WelcomeTitle = values[models.SettingWelcomeTitle]
	}
	if stored[models.SettingWelcomeText] && strings.TrimSpace(values[models.SettingWelcomeText]) != "" {
		resolved.WelcomeText = values[models.SettingWelcomeText]
	}
	return resolved, stored, nil
}

// SaveSettings stores the policy, replacing whatever was there.
func (s *Store) SaveSettings(ctx context.Context, settings models.Settings) error {
	return s.saveSettings(ctx, settings, nil)
}

// SaveSettingsWithBranding saves site policy and public identity in one
// transaction.
func (s *Store) SaveSettingsWithBranding(ctx context.Context, settings models.Settings, branding models.Branding) error {
	if err := ValidateBranding(branding); err != nil {
		return err
	}
	return s.saveSettings(ctx, settings, &branding)
}

func (s *Store) saveSettings(ctx context.Context, settings models.Settings, branding *models.Branding) error {
	if settings.AnonymousTTL <= 0 {
		return errors.New("store: the anonymous retention window must be positive")
	}
	if !settings.DefaultVisibility.Valid() {
		return fmt.Errorf("store: %q is not a visibility level", settings.DefaultVisibility)
	}

	return s.withTx(ctx, func(tx *sql.Tx) error {
		values := map[string]string{
			models.SettingAllowSignup:           strconv.FormatBool(settings.AllowSignup),
			models.SettingAllowAnonymousUploads: strconv.FormatBool(settings.AllowAnonymousUploads),
			models.SettingAnonymousTTLSeconds:   strconv.FormatInt(int64(settings.AnonymousTTL/time.Second), 10),
			models.SettingDefaultVisibility:     string(settings.DefaultVisibility),
			models.SettingInviteOnly:            strconv.FormatBool(settings.InviteOnly),
			models.SettingMaxTotalBytes:         strconv.FormatInt(settings.MaxTotalBytes, 10),
		}
		if branding != nil {
			values[models.SettingSiteName] = strings.TrimSpace(branding.SiteName)
			values[models.SettingSourceURL] = strings.TrimSpace(branding.SourceURL)
			values[models.SettingWelcomeTitle] = strings.TrimSpace(branding.WelcomeTitle)
			values[models.SettingWelcomeText] = strings.TrimSpace(branding.WelcomeText)
		}
		for key, value := range values {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
				ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
				key, value, nowUnix())
			if err != nil {
				return fmt.Errorf("save setting %s: %w", key, err)
			}
		}
		return nil
	})
}

// ValidateBranding checks the public text values before they are stored.
func ValidateBranding(branding models.Branding) error {
	checks := []struct {
		name      string
		value     string
		max       int
		multiline bool
	}{{"site name", branding.SiteName, 80, false}, {"welcome title", branding.WelcomeTitle, 120, false}, {"welcome text", branding.WelcomeText, 2000, true}}
	for _, check := range checks {
		if err := validateBrandingText(check.name, check.value, check.max, check.multiline); err != nil {
			return err
		}
	}
	return validateBrandingSourceURL(branding.SourceURL)
}

func validateBrandingText(name, value string, max int, multiline bool) error {
	if utf8.RuneCountInString(value) > max {
		return fmt.Errorf("%s must be no longer than %d characters", name, max)
	}
	for _, r := range value {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\r' || r == '\t')) {
			return fmt.Errorf("%s contains an unsupported control character", name)
		}
	}
	return nil
}

func validateBrandingSourceURL(value string) error {
	if len(value) > 512 {
		return errors.New("source link must be no longer than 512 characters")
	}
	if value == "" {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return errors.New("source link must be blank or an HTTP(S) URL")
	}
	return nil
}

// ClearSettings removes every stored override, so the configuration applies
// again. It is the way back to an environment-driven instance.
func (s *Store) ClearSettings(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM settings`)
	if err != nil {
		return 0, fmt.Errorf("clear settings: %w", err)
	}
	return res.RowsAffected()
}

// ApplyAnonymousRetention rewrites the deadline on existing anonymous uploads
// to match a new window, and reports how many it touched.
//
// Deadlines are fixed when an upload is stored, so without this a shortened
// window would only affect uploads made after the change, and a response to
// abuse would not take effect until the old deadlines passed.
//
// The deadline stays relative to when each file was uploaded, so the rule is
// the same for everything: an anonymous upload lives for the window, counted
// from when it arrived. Anything older than a newly shortened window therefore
// becomes collectable immediately.
func (s *Store) ApplyAnonymousRetention(ctx context.Context, window time.Duration) (int64, error) {
	seconds := int64(window / time.Second)

	res, err := s.db.ExecContext(ctx, `
		UPDATE files SET expires_at = created_at + ?
		WHERE user_id IS NULL
		  AND (expires_at IS NULL OR expires_at <> created_at + ?)`,
		seconds, seconds)
	if err != nil {
		return 0, fmt.Errorf("apply retention: %w", err)
	}
	return res.RowsAffected()
}
