// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"

	"imvault/internal/models"
)

// PasswordSet reports whether the account has a password somebody chose, as
// opposed to the random one a provider registration is given.
func (s *Store) PasswordSet(ctx context.Context, userID int64) (bool, error) {
	var set int
	if err := s.db.QueryRowContext(ctx,
		`SELECT password_set FROM users WHERE id = ?`, userID).Scan(&set); err != nil {
		return false, mapErr(err)
	}
	return set == 1, nil
}

// CreateProviderAccount creates an account that signs in through a provider:
// the account, its identity, the consumed invitation when there is one, and the
// record that nobody knows its password, all in one transaction.
//
// It is CreateUserWithIdentity plus that last step. Writing the flag after the
// account in a second statement would leave a window, and a failure, in which
// the account claims a password nobody has, which is the lockout this exists to
// prevent.
func (s *Store) CreateProviderAccount(ctx context.Context, in NewUser, issuer, subject, email string, inviteID *int64) (*models.User, error) {
	var user *models.User

	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if inviteID != nil {
			var err error
			in, err = invitedUser(ctx, tx, in, *inviteID)
			if err != nil {
				return err
			}
		}

		created, err := createUser(ctx, tx, in)
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_identities (user_id, issuer, subject, email, created_at, last_login)
			VALUES (?, ?, ?, ?, ?, ?)`,
			created.ID, issuer, subject, email, nowUnix(), nowUnix()); err != nil {
			if ok, _ := isUniqueViolation(err); ok {
				return ErrConflict
			}
			return fmt.Errorf("link identity: %w", err)
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET password_set = 0 WHERE id = ?`, created.ID); err != nil {
			return fmt.Errorf("mark password unset: %w", err)
		}

		user = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}
