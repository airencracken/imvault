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
func (s *Store) CreateInvite(ctx context.Context, createdBy int64, label, prefix, hash string,
	maxUses int, expiresAt *time.Time) (*models.Invite, error) {

	if maxUses < 0 {
		maxUses = 0
	}
	created := nowUnix()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO invites (prefix, code_hash, label, created_by, created_at, expires_at, max_uses)
		SELECT ?, ?, ?, u.id, ?, ?, ? FROM users u
		WHERE u.id = ? AND (u.role = 'admin' OR u.can_invite = 1)`,
		prefix, hash, label, created, nullableTime(expiresAt), maxUses, createdBy)
	if err != nil {
		if ok, col := isUniqueViolation(err); ok {
			return nil, fmt.Errorf("%w: invite %s", ErrConflict, col)
		}
		return nil, fmt.Errorf("insert invite: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return nil, ErrNotFound
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("invite id: %w", err)
	}

	return &models.Invite{
		ID:        id,
		Prefix:    prefix,
		Label:     label,
		CreatedBy: &createdBy,
		CreatedAt: toTime(created),
		ExpiresAt: expiresAt,
		MaxUses:   maxUses,
	}, nil
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
		`UPDATE invites SET revoked_at = ? WHERE id = ? AND created_by = ? AND revoked_at IS NULL`,
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
func redeemInvite(ctx context.Context, ex execer, id int64) error {
	res, err := ex.ExecContext(ctx, `
		UPDATE invites SET uses = uses + 1
		WHERE id = ?
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > ?)
		  AND (max_uses = 0 OR uses < max_uses)`, id, nowUnix())
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
		var inviterID sql.NullInt64
		var inviterName string
		if err := tx.QueryRowContext(ctx, `SELECT i.created_by, COALESCE(u.username, '')
			FROM invites i LEFT JOIN users u ON u.id = i.created_by WHERE i.id = ?`, inviteID).
			Scan(&inviterID, &inviterName); err != nil {
			return mapErr(err)
		}
		if err := redeemInvite(ctx, tx, inviteID); err != nil {
			return err
		}
		if inviterID.Valid {
			id := inviterID.Int64
			in.InvitedBy = &id
			in.InvitedByUsername = inviterName
		}
		id := inviteID
		in.InvitationID = &id
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
