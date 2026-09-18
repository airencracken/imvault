// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

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

	if raw, ok := stored[models.SettingAllowSignup]; ok {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			resolved.AllowSignup = parsed
			sources[models.SettingAllowSignup] = true
		}
	}
	if raw, ok := stored[models.SettingAllowAnonymousUploads]; ok {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			resolved.AllowAnonymousUploads = parsed
			sources[models.SettingAllowAnonymousUploads] = true
		}
	}
	if raw, ok := stored[models.SettingAnonymousTTLSeconds]; ok {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
			resolved.AnonymousTTL = time.Duration(seconds) * time.Second
			sources[models.SettingAnonymousTTLSeconds] = true
		}
	}
	if raw, ok := stored[models.SettingInviteOnly]; ok {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			resolved.InviteOnly = parsed
			sources[models.SettingInviteOnly] = true
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

// SaveSettings stores the policy, replacing whatever was there.
func (s *Store) SaveSettings(ctx context.Context, settings models.Settings) error {
	if settings.AnonymousTTL <= 0 {
		return errors.New("store: the anonymous retention window must be positive")
	}
	if !settings.DefaultVisibility.Valid() {
		return fmt.Errorf("store: %q is not a visibility level", settings.DefaultVisibility)
	}

	return s.withTx(ctx, func(tx *sql.Tx) error {
		for key, value := range map[string]string{
			models.SettingAllowSignup:           strconv.FormatBool(settings.AllowSignup),
			models.SettingAllowAnonymousUploads: strconv.FormatBool(settings.AllowAnonymousUploads),
			models.SettingAnonymousTTLSeconds:   strconv.FormatInt(int64(settings.AnonymousTTL/time.Second), 10),
			models.SettingDefaultVisibility:     string(settings.DefaultVisibility),
			models.SettingInviteOnly:            strconv.FormatBool(settings.InviteOnly),
		} {
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
