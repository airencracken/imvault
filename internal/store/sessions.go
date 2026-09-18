// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"imvault/internal/models"
) // CreateSession records a login session. Only the hash of the session token is
// stored, so a database leak does not hand over live sessions.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
		VALUES (?, ?, ?, ?)`,
		tokenHash, userID, nowUnix(), ts(expiresAt),
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// userColumnsPrefixed is userColumns qualified with the alias "u", derived so
// the two can never drift apart.
var userColumnsPrefixed = prefixColumns("u", userColumns)

// prefixColumns qualifies a comma-separated column list with a table alias.
func prefixColumns(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = alias + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}

// UserBySession resolves a session token hash to its account, ignoring
// sessions that have expired.
func (s *Store) UserBySession(ctx context.Context, tokenHash string, at time.Time) (*models.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+userColumnsPrefixed+`
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`,
		tokenHash, ts(at),
	)
	u, err := scanUser(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return u, nil
}

// DeleteSession removes a single session (logout).
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions prunes sessions that lapsed before at.
func (s *Store) DeleteExpiredSessions(ctx context.Context, at time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, ts(at))
	if err != nil {
		return 0, fmt.Errorf("prune sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune sessions rows: %w", err)
	}
	return n, nil
}

// DeleteSessionsForUser signs an account out everywhere, which is what
// disabling it should do.
func (s *Store) DeleteSessionsForUser(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}
