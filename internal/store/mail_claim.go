// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"time"
)

// ClaimMail takes one queued message for delivery, reporting whether this
// caller got it.
//
// A message is delivered from two places: straight away, by the request that
// queued it, and later by the retry sweep. Without a claim both could pick up
// the same row while a slow relay was still answering the first, and the
// recipient got the message twice. The claim pushes the next attempt out to
// until, in the same statement that checks the row is due, so only one caller
// can win it; the attempt that follows records the real outcome.
func (s *Store) ClaimMail(ctx context.Context, id int64, now, until time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE outbound_mail SET next_attempt_at = ?
		WHERE id = ? AND sent_at IS NULL AND failed_at IS NULL AND next_attempt_at <= ?`,
		ts(until), id, ts(now))
	if err != nil {
		return false, fmt.Errorf("claim mail: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim mail rows: %w", err)
	}
	return affected == 1, nil
}
