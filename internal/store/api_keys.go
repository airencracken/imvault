// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"imvault/internal/models"
)

const apiKeyColumns = `k.id, k.user_id, k.name, k.prefix, k.key_hash, k.created_at,
	k.last_used_at, k.expires_at, COALESCE(u.username, '')`

const apiKeyFrom = `FROM api_keys k LEFT JOIN users u ON u.id = k.user_id`

func scanAPIKey(sc rowScanner) (*models.APIKey, error) {
	var (
		k         models.APIKey
		created   int64
		lastUsed  sql.NullInt64
		expiresAt sql.NullInt64
	)
	if err := sc.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.KeyHash,
		&created, &lastUsed, &expiresAt, &k.Username); err != nil {
		return nil, err
	}
	k.CreatedAt = toTime(created)
	k.LastUsedAt = timePtr(lastUsed)
	k.ExpiresAt = timePtr(expiresAt)
	return &k, nil
}

// CreateAPIKey stores a new key. Only the hash is persisted.
func (s *Store) CreateAPIKey(ctx context.Context, userID int64, name, prefix, keyHash string, expiresAt *time.Time) (*models.APIKey, error) {
	created := nowUnix()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO api_keys (user_id, name, prefix, key_hash, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		userID, name, prefix, keyHash, created, nullableTime(expiresAt),
	)
	if err != nil {
		if ok, _ := isUniqueViolation(err); ok {
			return nil, fmt.Errorf("%w: api key prefix", ErrConflict)
		}
		return nil, fmt.Errorf("insert api key: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("api key id: %w", err)
	}

	return &models.APIKey{
		ID:        id,
		UserID:    userID,
		Name:      name,
		Prefix:    prefix,
		KeyHash:   keyHash,
		CreatedAt: toTime(created),
		ExpiresAt: expiresAt,
	}, nil
}

// APIKeyByPrefix resolves the single row for a public key prefix. The caller
// must still verify the presented secret against the returned hash.
func (s *Store) APIKeyByPrefix(ctx context.Context, prefix string) (*models.APIKey, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+apiKeyColumns+` `+apiKeyFrom+` WHERE k.prefix = ?`, prefix)
	k, err := scanAPIKey(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return k, nil
}

// APIKeysByUser lists an account's keys, newest first.
func (s *Store) APIKeysByUser(ctx context.Context, userID int64) ([]*models.APIKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+apiKeyColumns+` `+apiKeyFrom+`
		 WHERE k.user_id = ?
		 ORDER BY k.created_at DESC, k.id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var keys []*models.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, fmt.Errorf("scan api key: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// DeleteAPIKey revokes a key. The user id scopes the delete so one account
// cannot revoke another's key.
func (s *Store) DeleteAPIKey(ctx context.Context, id, userID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM api_keys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete api key rows: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchAPIKey records that a key was used.
func (s *Store) TouchAPIKey(ctx context.Context, id int64, at time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, ts(at), id); err != nil {
		return fmt.Errorf("touch api key: %w", err)
	}
	return nil
}

// DeleteExpiredAPIKeys removes keys that lapsed before at.
func (s *Store) DeleteExpiredAPIKeys(ctx context.Context, at time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM api_keys WHERE expires_at IS NOT NULL AND expires_at <= ?`, ts(at))
	if err != nil {
		return 0, fmt.Errorf("prune api keys: %w", err)
	}
	return res.RowsAffected()
}

// DeleteAPIKeysForUser revokes every key an account holds.
func (s *Store) DeleteAPIKeysForUser(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete user api keys: %w", err)
	}
	return nil
}
