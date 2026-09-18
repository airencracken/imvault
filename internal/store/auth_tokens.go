// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"imvault/internal/models"
)

// TokenPurpose distinguishes the flows that use one-time tokens, so a token
// minted for one can never be redeemed for another.
type TokenPurpose string

const (
	// TokenPasswordReset authorises setting a new password.
	TokenPasswordReset TokenPurpose = "password_reset"
	// TokenEmailVerify confirms ownership of an email address.
	TokenEmailVerify TokenPurpose = "email_verify"
	// TokenLoginSecondFactor carries a password check through to the code
	// prompt, so the code step has proof that the password was already given.
	TokenLoginSecondFactor TokenPurpose = "login_second_factor"
)

// CreateAuthToken records a one-time token. The caller passes the digest, never
// the token itself.
func (s *Store) CreateAuthToken(ctx context.Context, userID int64, purpose TokenPurpose, tokenHash string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_tokens (token_hash, user_id, purpose, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		tokenHash, userID, string(purpose), nowUnix(), ts(expiresAt),
	)
	if err != nil {
		return fmt.Errorf("insert auth token: %w", err)
	}
	return nil
}

// ConsumeAuthToken redeems a token, returning the account it belonged to.
//
// Marking it used and reading the owner happen in one transaction, so a token
// cannot be redeemed twice even if two requests arrive together. Expired,
// already-used and unknown tokens are all reported as ErrNotFound, so a caller
// cannot tell them apart.
func (s *Store) ConsumeAuthToken(ctx context.Context, tokenHash string, purpose TokenPurpose, at time.Time) (*models.User, error) {
	var userID int64

	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var (
			usedAt  sql.NullInt64
			expires int64
		)
		err := tx.QueryRowContext(ctx, `
			SELECT user_id, used_at, expires_at FROM auth_tokens
			WHERE token_hash = ? AND purpose = ?`,
			tokenHash, string(purpose),
		).Scan(&userID, &usedAt, &expires)
		if err != nil {
			return mapErr(err)
		}

		if usedAt.Valid || expires <= ts(at) {
			return ErrNotFound
		}

		result, err := tx.ExecContext(ctx,
			`UPDATE auth_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL`,
			ts(at), tokenHash)
		if err != nil {
			return fmt.Errorf("mark auth token used: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("mark auth token used rows: %w", err)
		}
		if affected == 0 {
			return ErrNotFound // lost a race to another request
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return user, nil
}

// AuthTokenValid reports whether a token could still be redeemed, without
// consuming it.
//
// The reset form is reached by a GET, and a user may well load it twice, so
// validity has to be checkable without spending the token.
func (s *Store) AuthTokenValid(ctx context.Context, tokenHash string, purpose TokenPurpose, at time.Time) (*models.User, error) {
	var userID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id FROM auth_tokens
		WHERE token_hash = ? AND purpose = ? AND used_at IS NULL AND expires_at > ?`,
		tokenHash, string(purpose), ts(at),
	).Scan(&userID)
	if err != nil {
		return nil, mapErr(err)
	}

	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return user, nil
}

// DeleteAuthToken removes one token by its digest, whatever its purpose.
func (s *Store) DeleteAuthToken(ctx context.Context, tokenHash string, purpose TokenPurpose) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM auth_tokens WHERE token_hash = ? AND purpose = ?`,
		tokenHash, string(purpose)); err != nil {
		return fmt.Errorf("delete auth token: %w", err)
	}
	return nil
}

// DeleteAuthTokensForUser clears an account's outstanding tokens of one kind,
// which is what issuing a new one should do so only the latest link works.
func (s *Store) DeleteAuthTokensForUser(ctx context.Context, userID int64, purpose TokenPurpose) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM auth_tokens WHERE user_id = ? AND purpose = ?`, userID, string(purpose)); err != nil {
		return fmt.Errorf("delete auth tokens: %w", err)
	}
	return nil
}

// DeleteExpiredAuthTokens prunes tokens that have lapsed or been redeemed.
func (s *Store) DeleteExpiredAuthTokens(ctx context.Context, at time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM auth_tokens WHERE expires_at <= ? OR used_at IS NOT NULL`, ts(at))
	if err != nil {
		return 0, fmt.Errorf("prune auth tokens: %w", err)
	}
	return res.RowsAffected()
}

// SetPassword replaces an account's password hash.
func (s *Store) SetPassword(ctx context.Context, userID int64, passwordHash string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	return nil
}

// SetEmail records an address and whether it has been confirmed.
func (s *Store) SetEmail(ctx context.Context, userID int64, email string, verified bool) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET email = ?, email_verified = ? WHERE id = ?`,
		email, boolToInt(verified), userID); err != nil {
		return fmt.Errorf("set email: %w", err)
	}
	return nil
}
