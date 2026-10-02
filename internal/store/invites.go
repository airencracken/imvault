// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"imvault/internal/models"
)

// ErrInviteUnusable reports an invitation that is revoked, expired, or already
// used up. The three are deliberately one error: telling a stranger which it is
// would confirm that a code exists.
var ErrInviteUnusable = errors.New("invite unusable")

// ErrInviteTooBroad means a delegated issuer asked for more than delegation
// allows: too many uses, or a lifetime past the ceiling.
var ErrInviteTooBroad = errors.New("store: invitation exceeds what a delegated issuer may grant")

// Limits on codes issued by members with invitation permission. An
// administrator is trusted with the instance and is not limited; a member
// handing out an unlimited, permanent code would be.
const (
	MaxDelegatedInviteUses     = 25
	MaxDelegatedInviteLifetime = 30 * 24 * time.Hour
)

// execer is the subset of a database handle that a statement needs. It lets the
// same helper run inside a transaction or on its own.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

const inviteColumns = `i.id, i.prefix, i.label, i.created_by, i.created_at, i.expires_at,
	i.max_uses, i.uses, i.revoked_at, COALESCE(u.username, '')`

func scanInvite(sc rowScanner) (*models.Invite, error) {
	var (
		inv       models.Invite
		createdBy sql.NullInt64
		expires   sql.NullInt64
		revoked   sql.NullInt64
		created   int64
	)
	if err := sc.Scan(&inv.ID, &inv.Prefix, &inv.Label, &createdBy, &created, &expires,
		&inv.MaxUses, &inv.Uses, &revoked, &inv.Creator); err != nil {
		return nil, err
	}
	if createdBy.Valid {
		id := createdBy.Int64
		inv.CreatedBy = &id
	}
	inv.CreatedAt = toTime(created)
	inv.ExpiresAt = timePtr(expires)
	inv.RevokedAt = timePtr(revoked)
	return &inv, nil
}

// CreateInvite records a freshly minted code. Only the digest is stored; the
// code itself is shown once and never kept.
//
// The issuer's permission is checked in the same statement that inserts the
// row. A member who is not an administrator is held to the delegated limits:
// between one and MaxDelegatedInviteUses uses, and a lifetime of at most
// MaxDelegatedInviteLifetime, which is also the lifetime given when none is
// asked for. The web form applies the same limits; this is the backstop.
func (s *Store) CreateInvite(ctx context.Context, createdBy int64, label, prefix, hash string,
	maxUses int, expiresAt *time.Time) (*models.Invite, error) {

	if maxUses < 0 {
		maxUses = 0
	}
	created := nowUnix()
	ceiling := created + int64(MaxDelegatedInviteLifetime/time.Second)

	var invite *models.Invite
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		role, err := inviteIssuerRole(ctx, tx, createdBy)
		if err != nil {
			return err
		}
		if role != models.RoleAdmin {
			if maxUses < 1 || maxUses > MaxDelegatedInviteUses ||
				(expiresAt != nil && ts(*expiresAt) > ceiling) {
				return ErrInviteTooBroad
			}
			if expiresAt == nil {
				limit := toTime(ceiling)
				expiresAt = &limit
			}
		}

		res, err := tx.ExecContext(ctx, `
			INSERT INTO invites (prefix, code_hash, label, created_by, created_at, expires_at, max_uses)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			prefix, hash, label, createdBy, created, nullableTime(expiresAt), maxUses)
		if err != nil {
			if ok, col := isUniqueViolation(err); ok {
				return fmt.Errorf("%w: invite %s", ErrConflict, col)
			}
			return fmt.Errorf("insert invite: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("invite id: %w", err)
		}
		invite = &models.Invite{
			ID:        id,
			Prefix:    prefix,
			Label:     label,
			CreatedBy: &createdBy,
			CreatedAt: toTime(created),
			ExpiresAt: expiresAt,
			MaxUses:   maxUses,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return invite, nil
}

// inviteIssuerRole returns the role of an account that may currently issue
// invitations, or ErrNotFound for one that may not: missing, disabled, or
// neither an administrator nor granted the permission.
func inviteIssuerRole(ctx context.Context, tx *sql.Tx, userID int64) (models.Role, error) {
	var role string
	err := tx.QueryRowContext(ctx, `
		SELECT role FROM users
		WHERE id = ? AND disabled = 0 AND (role = 'admin' OR can_invite = 1)`, userID).Scan(&role)
	if err != nil {
		return "", mapErr(err)
	}
	return models.ParseRole(role), nil
}

// revokeOpenInvitesByCreator withdraws every code an account issued that is
// still open. It runs inside the transaction that takes the account's
// permission away, so no code outlives the authority that issued it.
func revokeOpenInvitesByCreator(ctx context.Context, tx *sql.Tx, userID int64) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE invites SET revoked_at = ? WHERE created_by = ? AND revoked_at IS NULL`,
		nowUnix(), userID); err != nil {
		return fmt.Errorf("revoke invitations: %w", err)
	}
	return nil
}

// InviteByPrefix finds the row a presented code redeems against. The caller
// still has to verify the digest; this only narrows the search to one row.
func (s *Store) InviteByPrefix(ctx context.Context, prefix string) (*models.Invite, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+inviteColumns+`
		 FROM invites i LEFT JOIN users u ON u.id = i.created_by
		 WHERE i.prefix = ?`, prefix)
	inv, err := scanInvite(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return inv, nil
}

// InviteCodeHash returns the stored digest for a code prefix.
//
// It is separate from the invitation itself so a digest only exists in the one
// place that has to compare against it, rather than travelling with every row
// that gets listed or rendered.
func (s *Store) InviteCodeHash(ctx context.Context, prefix string) (string, error) {
	var hash string
	if err := s.db.QueryRowContext(ctx,
		`SELECT code_hash FROM invites WHERE prefix = ?`, prefix).Scan(&hash); err != nil {
		return "", mapErr(err)
	}
	return hash, nil
}

// ListInvites returns invitations, newest first, plus the total count.
func (s *Store) ListInvites(ctx context.Context, limit, offset int) ([]*models.Invite, int, error) {
	return s.listInvites(ctx, 0, true, limit, offset)
}

// ListInvitesByCreator returns only invitations issued by one non-admin user.
func (s *Store) ListInvitesByCreator(ctx context.Context, creatorID int64, limit, offset int) ([]*models.Invite, int, error) {
	return s.listInvites(ctx, creatorID, false, limit, offset)
}

func (s *Store) listInvites(ctx context.Context, creatorID int64, all bool, limit, offset int) ([]*models.Invite, int, error) {
	filter := ""
	args := []any{}
	if !all {
		filter = "WHERE i.created_by = ?"
		args = append(args, creatorID)
	}
	args = append(args, limit, offset)
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invites i `+filter, args[:len(args)-2]...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count invites: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+inviteColumns+`
		 FROM invites i LEFT JOIN users u ON u.id = i.created_by `+filter+`
		 ORDER BY i.created_at DESC, i.id DESC
		 LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list invites: %w", err)
	}
	defer rows.Close()

	var invites []*models.Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan invite: %w", err)
		}
		invites = append(invites, inv)
	}
	return invites, total, rows.Err()
}

// RevokeInviteByCreator limits delegated issuers to revoking their own codes.
func (s *Store) RevokeInviteByCreator(ctx context.Context, id, creatorID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE invites SET revoked_at = ? WHERE id = ? AND created_by = ? AND revoked_at IS NULL
		 AND EXISTS (SELECT 1 FROM users u WHERE u.id = invites.created_by
		 AND u.disabled = 0 AND (u.role = 'admin' OR u.can_invite = 1))`,
		nowUnix(), id, creatorID)
	if err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	if affected, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	} else if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// InviteByID loads one invitation.
func (s *Store) InviteByID(ctx context.Context, id int64) (*models.Invite, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+inviteColumns+`
		 FROM invites i LEFT JOIN users u ON u.id = i.created_by
		 WHERE i.id = ?`, id)
	inv, err := scanInvite(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return inv, nil
}

// RevokeInvite withdraws a code. Revoking twice is not an error: the outcome
// the caller wanted is already true.
func (s *Store) RevokeInvite(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE invites SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		nowUnix(), id); err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	return nil
}

// redeemInvite consumes one use, refusing a code that is no longer usable.
//
// The checks live in the UPDATE rather than before it so that two people racing
// to redeem the last use of a code cannot both succeed: the condition and the
// increment are one statement.
//
// A code with no issuer is refused. Deleting an account revokes its codes, so
// none should exist; this is the second lock on the same door, and it is what
// keeps a disabled issuer's codes from coming back to life when the account is
// then deleted.
func redeemInvite(ctx context.Context, ex execer, id int64) error {
	res, err := ex.ExecContext(ctx, `
		UPDATE invites SET uses = uses + 1
		WHERE id = ?
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > ?)
		  AND (max_uses = 0 OR uses < max_uses)
		  AND created_by IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = invites.created_by AND u.disabled = 1)`, id, nowUnix())
	if err != nil {
		return fmt.Errorf("redeem invite: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("redeem invite: %w", err)
	}
	if affected != 1 {
		return ErrInviteUnusable
	}
	return nil
}

// RegisterWithInvite creates an account and consumes one use of an invitation
// in a single transaction.
//
// Doing both together is the point. Registering with a username that is already
// taken must not burn the code, and a use must never be consumed without an
// account to show for it, which is exactly what two separate statements would
// allow.
func (s *Store) RegisterWithInvite(ctx context.Context, in NewUser, inviteID int64) (*models.User, error) {
	var user *models.User

	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		in, err = invitedUser(ctx, tx, in, inviteID)
		if err != nil {
			return err
		}
		created, err := createUser(ctx, tx, in)
		if err != nil {
			return err
		}
		user = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

// invitedUser consumes the code and records its issuer in the same transaction
// as either a password or provider registration.
func invitedUser(ctx context.Context, tx *sql.Tx, in NewUser, inviteID int64) (NewUser, error) {
	var inviterID sql.NullInt64
	var inviterName string
	if err := tx.QueryRowContext(ctx, `SELECT i.created_by, COALESCE(u.username, '')
		FROM invites i LEFT JOIN users u ON u.id = i.created_by WHERE i.id = ?`, inviteID).
		Scan(&inviterID, &inviterName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return in, ErrInviteUnusable
		}
		return in, mapErr(err)
	}
	if err := redeemInvite(ctx, tx, inviteID); err != nil {
		return in, err
	}
	in.InvitedBy, in.InvitedByUsername = nil, inviterName
	if inviterID.Valid {
		in.InvitedBy = &inviterID.Int64
	}
	in.InvitationID = &inviteID
	return in, nil
}
