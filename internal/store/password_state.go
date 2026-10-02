// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import "context"

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
