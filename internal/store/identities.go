// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"

	"imvault/internal/models"
)

const identityColumns = `id, user_id, issuer, subject, email, created_at, last_login`

func scanIdentity(sc rowScanner) (*models.Identity, error) {
	var (
		identity  models.Identity
		lastLogin sql.NullInt64
		created   int64
	)
	if err := sc.Scan(&identity.ID, &identity.UserID, &identity.Issuer, &identity.Subject,
		&identity.Email, &created, &lastLogin); err != nil {
		return nil, err
	}
	identity.CreatedAt = toTime(created)
	identity.LastLogin = timePtr(lastLogin)
	return &identity, nil
}

// IdentityBySubject finds the link a provider's assertion redeems.
func (s *Store) IdentityBySubject(ctx context.Context, issuer, subject string) (*models.Identity, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+identityColumns+`
		 FROM user_identities WHERE issuer = ? AND subject = ?`, issuer, subject)
	identity, err := scanIdentity(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return identity, nil
}

// IdentitiesByUser lists the links an account has.
func (s *Store) IdentitiesByUser(ctx context.Context, userID int64) ([]*models.Identity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+identityColumns+`
		 FROM user_identities WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	defer rows.Close()

	var identities []*models.Identity
	for rows.Next() {
		identity, err := scanIdentity(rows)
		if err != nil {
			return nil, fmt.Errorf("scan identity: %w", err)
		}
		identities = append(identities, identity)
	}
	return identities, rows.Err()
}

// LinkIdentity records that a provider assertion belongs to an account.
//
// The unique constraint is on (issuer, subject), so an identity that is already
// spoken for cannot be claimed by a second account however the two callbacks
// interleave. It returns ErrConflict when it already belongs to somebody else.
func (s *Store) LinkIdentity(ctx context.Context, userID int64, issuer, subject, email string) (*models.Identity, error) {
	created := nowUnix()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO user_identities (user_id, issuer, subject, email, created_at, last_login)
		VALUES (?, ?, ?, ?, ?, ?)`,
		userID, issuer, subject, email, created, created)
	if err != nil {
		if ok, _ := isUniqueViolation(err); ok {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("link identity: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("identity id: %w", err)
	}

	at := toTime(created)
	return &models.Identity{
		ID:        id,
		UserID:    userID,
		Issuer:    issuer,
		Subject:   subject,
		Email:     email,
		CreatedAt: at,
		LastLogin: &at,
	}, nil
}

// TouchIdentity records a successful sign-in through a link.
func (s *Store) TouchIdentity(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE user_identities SET last_login = ? WHERE id = ?`, nowUnix(), id); err != nil {
		return fmt.Errorf("touch identity: %w", err)
	}
	return nil
}

// UnlinkIdentity removes a link. It requires the owning account as well as the
// id, so a link cannot be removed by guessing a number.
func (s *Store) UnlinkIdentity(ctx context.Context, id, userID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM user_identities WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("unlink identity: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("unlink identity: %w", err)
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

// CreateUserWithIdentity creates an account and links a provider assertion to
// it, consuming an invitation when one is given, all in one transaction.
//
// The identity and the account have to arrive together. Creating the account
// first and linking after would leave an orphan every time the link failed, and
// the link is the only reason the account is being made.
func (s *Store) CreateUserWithIdentity(ctx context.Context, in NewUser, issuer, subject, email string, inviteID *int64) (*models.User, error) {
	var user *models.User

	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if inviteID != nil {
			if err := redeemInvite(ctx, tx, *inviteID); err != nil {
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

		user = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}
