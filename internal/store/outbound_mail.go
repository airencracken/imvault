// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"imvault/internal/models"
)

const outboundMailColumns = `id, recipient, subject, body, created_at, attempts,
	next_attempt_at, sent_at, failed_at, last_error`

func scanOutboundMail(sc rowScanner) (*models.OutboundMail, error) {
	var (
		m        models.OutboundMail
		created  int64
		next     int64
		sentAt   sql.NullInt64
		failedAt sql.NullInt64
	)

	if err := sc.Scan(&m.ID, &m.Recipient, &m.Subject, &m.Body, &created,
		&m.Attempts, &next, &sentAt, &failedAt, &m.LastError); err != nil {
		return nil, err
	}

	m.CreatedAt = toTime(created)
	m.NextAttemptAt = toTime(next)
	m.SentAt = timePtr(sentAt)
	m.FailedAt = timePtr(failedAt)
	return &m, nil
}

// EnqueueMail records a message for delivery, due immediately.
func (s *Store) EnqueueMail(ctx context.Context, recipient, subject, body string) (*models.OutboundMail, error) {
	now := nowUnix()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO outbound_mail (recipient, subject, body, created_at, next_attempt_at)
		VALUES (?, ?, ?, ?, ?)`,
		recipient, subject, body, now, now)
	if err != nil {
		return nil, fmt.Errorf("enqueue mail: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("enqueue mail id: %w", err)
	}

	return &models.OutboundMail{
		ID:            id,
		Recipient:     recipient,
		Subject:       subject,
		Body:          body,
		CreatedAt:     toTime(now),
		NextAttemptAt: toTime(now),
	}, nil
}

// DueMail returns unsent, unfailed messages whose next attempt has arrived.
func (s *Store) DueMail(ctx context.Context, at time.Time, limit int) ([]*models.OutboundMail, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+outboundMailColumns+` FROM outbound_mail
		WHERE sent_at IS NULL AND failed_at IS NULL AND next_attempt_at <= ?
		ORDER BY next_attempt_at, id
		LIMIT ?`, ts(at), limit)
	if err != nil {
		return nil, fmt.Errorf("list due mail: %w", err)
	}
	defer rows.Close()

	return collectOutboundMail(rows)
}

// ListMail returns recent messages, newest first, for the admin view.
func (s *Store) ListMail(ctx context.Context, limit int) ([]*models.OutboundMail, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+outboundMailColumns+` FROM outbound_mail
		ORDER BY id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list mail: %w", err)
	}
	defer rows.Close()

	return collectOutboundMail(rows)
}

func collectOutboundMail(rows *sql.Rows) ([]*models.OutboundMail, error) {
	var messages []*models.OutboundMail
	for rows.Next() {
		m, err := scanOutboundMail(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mail: %w", err)
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// MarkMailSent records a successful delivery.
func (s *Store) MarkMailSent(ctx context.Context, id int64, at time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE outbound_mail SET sent_at = ?, last_error = '' WHERE id = ?`,
		ts(at), id); err != nil {
		return fmt.Errorf("mark mail sent: %w", err)
	}
	return nil
}

// MarkMailAttempt records a failed delivery, scheduling the next try or giving
// up by setting failedAt.
func (s *Store) MarkMailAttempt(ctx context.Context, id int64, attempts int, nextAttempt time.Time, lastError string, failedAt *time.Time) error {
	if len(lastError) > 500 {
		lastError = lastError[:500]
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE outbound_mail
		SET attempts = ?, next_attempt_at = ?, last_error = ?, failed_at = ?
		WHERE id = ?`,
		attempts, ts(nextAttempt), lastError, nullableTime(failedAt), id); err != nil {
		return fmt.Errorf("mark mail attempt: %w", err)
	}
	return nil
}

// RetryMail puts a failed message back in the queue.
func (s *Store) RetryMail(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE outbound_mail
		SET failed_at = NULL, attempts = 0, next_attempt_at = ?
		WHERE id = ? AND sent_at IS NULL`,
		nowUnix(), id)
	if err != nil {
		return fmt.Errorf("retry mail: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("retry mail rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMail discards a message outright.
func (s *Store) DeleteMail(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM outbound_mail WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete mail: %w", err)
	}
	return nil
}

// MailBacklog counts what is still owed and what has given up.
func (s *Store) MailBacklog(ctx context.Context) (pending, failed int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM outbound_mail WHERE sent_at IS NULL AND failed_at IS NULL),
			(SELECT COUNT(*) FROM outbound_mail WHERE failed_at IS NOT NULL)`).
		Scan(&pending, &failed)
	if err != nil {
		return 0, 0, fmt.Errorf("mail backlog: %w", err)
	}
	return pending, failed, nil
}

// PruneSentMail removes delivered messages older than the cutoff, so the table
// does not grow without bound.
func (s *Store) PruneSentMail(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM outbound_mail WHERE sent_at IS NOT NULL AND sent_at <= ?`, ts(before))
	if err != nil {
		return 0, fmt.Errorf("prune sent mail: %w", err)
	}
	return res.RowsAffected()
}
