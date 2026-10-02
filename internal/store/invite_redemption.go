// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"

	"imvault/internal/models"
)

// hashScanner appends one more destination, the code's digest, to whatever a
// row scan asks for, so an invitation and its digest can come from one query
// while scanInvite stays the only place that knows the invitation's columns.
type hashScanner struct {
	row  rowScanner
	hash *string
}

func (h hashScanner) Scan(dest ...any) error {
	return h.row.Scan(append(dest, h.hash)...)
}

// InviteForRedemption returns the invitation a presented code's prefix names,
// together with the stored digest the code has to match, in one query.
//
// Looking a row up by prefix narrows the search; it does not authenticate
// anything. The caller verifies the digest before trusting the row.
func (s *Store) InviteForRedemption(ctx context.Context, prefix string) (*models.Invite, string, error) {
	var hash string
	row := s.db.QueryRowContext(ctx,
		`SELECT `+inviteColumns+`, i.code_hash
		 FROM invites i LEFT JOIN users u ON u.id = i.created_by
		 WHERE i.prefix = ?`, prefix)
	inv, err := scanInvite(hashScanner{row: row, hash: &hash})
	if err != nil {
		return nil, "", mapErr(err)
	}
	return inv, hash, nil
}
