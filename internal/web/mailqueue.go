// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"log/slog"
	"time"

	"imvault/internal/mail"
	"imvault/internal/models"
	"imvault/internal/store"
)

// Mail queue retry policy.
const (
	// mailBatchSize is how many messages one pass will attempt.
	mailBatchSize = 20
	// mailBackoffBase is the delay after the first failure; it multiplies by
	// four each time, so the schedule is roughly 1m, 4m, 16m, 1h, 4h.
	mailBackoffBase = time.Minute
	// mailBackoffMax caps the delay between attempts.
	mailBackoffMax = 6 * time.Hour
	// sentMailRetention is how long delivered messages are kept for reference.
	sentMailRetention = 7 * 24 * time.Hour
)

// mailQueue wraps a Sender so that a delivery failure delays a message rather
// than losing it.
//
// Every message is written to the database before the first attempt, which is
// what makes the queue survive both a relay outage and a restart. Send reports
// success once the message is durably queued, not once it is delivered, so a
// caller can honestly tell somebody to check their inbox.
type mailQueue struct {
	store       *store.Store
	sender      mail.Sender
	log         *slog.Logger
	maxAttempts int
	interval    time.Duration
	// now is injectable so tests can drive the backoff schedule.
	now func() time.Time
}

// NewMailQueue wraps a sender in a durable, retrying queue.
func NewMailQueue(st *store.Store, sender mail.Sender, maxAttempts int, interval time.Duration, log *slog.Logger) mail.Sender {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if interval <= 0 {
		interval = time.Minute
	}
	return &mailQueue{
		store:       st,
		sender:      sender,
		log:         log,
		maxAttempts: maxAttempts,
		interval:    interval,
		now:         time.Now,
	}
}

// Enabled mirrors the underlying sender.
func (q *mailQueue) Enabled() bool { return q.sender.Enabled() }

// Send queues a message and makes one delivery attempt.
func (q *mailQueue) Send(ctx context.Context, msg mail.Message) error {
	record, err := q.store.EnqueueMail(ctx, msg.To, msg.Subject, msg.Body)
	if err != nil {
		// The queue itself is broken. Trying the relay directly is a better
		// outcome than dropping the message, even though it will not be
		// retried.
		q.log.Error("could not queue mail; attempting direct delivery", "error", err)
		return q.sender.Send(ctx, msg)
	}

	q.attempt(ctx, record)
	return nil
}

// attempt tries to deliver one queued message and records the outcome.
func (q *mailQueue) attempt(ctx context.Context, record *models.OutboundMail) {
	if !q.sender.Enabled() {
		return
	}

	err := q.sender.Send(ctx, mail.Message{
		To:      record.Recipient,
		Subject: record.Subject,
		Body:    record.Body,
	})

	now := q.now().UTC()

	if err == nil {
		if markErr := q.store.MarkMailSent(ctx, record.ID, now); markErr != nil {
			q.log.Error("could not record mail delivery", "id", record.ID, "error", markErr)
		}
		return
	}

	attempts := record.Attempts + 1

	if attempts >= q.maxAttempts {
		failedAt := now
		q.log.Error("giving up on a mail message",
			"id", record.ID,
			"recipient", record.Recipient,
			"attempts", attempts,
			"error", err,
		)
		if markErr := q.store.MarkMailAttempt(ctx, record.ID, attempts, now, err.Error(), &failedAt); markErr != nil {
			q.log.Error("could not record mail failure", "id", record.ID, "error", markErr)
		}
		return
	}

	next := now.Add(mailBackoff(attempts))
	q.log.Warn("mail delivery failed; will retry",
		"id", record.ID,
		"recipient", record.Recipient,
		"attempt", attempts,
		"retry_at", next.Format(time.RFC3339),
		"error", err,
	)

	if markErr := q.store.MarkMailAttempt(ctx, record.ID, attempts, next, err.Error(), nil); markErr != nil {
		q.log.Error("could not record mail attempt", "id", record.ID, "error", markErr)
	}
}

// mailBackoff returns the delay before the given attempt number, counting from
// one. It grows by a factor of four and is capped.
func mailBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	delay := mailBackoffBase
	for i := 1; i < attempt; i++ {
		delay *= 4
		if delay >= mailBackoffMax {
			return mailBackoffMax
		}
	}
	if delay > mailBackoffMax {
		return mailBackoffMax
	}
	return delay
}

// StartMailRetry drains the queue until ctx is cancelled, and keeps sweeping
// afterwards for messages that arrive outside the tick.
func (s *Server) StartMailRetry(ctx context.Context) {
	if !s.mail.Enabled() {
		return
	}

	// Pass the backlog once at startup, in case the previous run was stopped
	// with messages still queued.
	s.drainMail(ctx)

	ticker := time.NewTicker(s.mailRetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.drainMail(ctx)
		}
	}
}

// drainMail attempts every message that is due.
func (s *Server) drainMail(ctx context.Context) {
	queue, ok := s.mail.(*mailQueue)
	if !ok {
		return
	}

	// Use the queue's clock, so a test can drive the backoff schedule without
	// waiting it out.
	now := time.Now()
	if queue.now != nil {
		now = queue.now()
	}

	messages, err := s.store.DueMail(ctx, now, mailBatchSize)
	if err != nil {
		s.log.Error("mail retry: list due", "error", err)
		return
	}

	for _, message := range messages {
		if ctx.Err() != nil {
			return
		}
		queue.attempt(ctx, message)
	}
}

// mailQueueFor exposes the queue for tests, which need to drive the schedule.
func (s *Server) mailQueueFor() *mailQueue {
	queue, _ := s.mail.(*mailQueue)
	return queue
}
