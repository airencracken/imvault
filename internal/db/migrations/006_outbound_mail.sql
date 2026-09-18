-- Durable outbound mail.
--
-- Previously a message was handed straight to the relay: if the relay was
-- unreachable the message was logged and lost, and the person waiting for a
-- reset link simply never received one. Messages are now written here first and
-- delivered by a retrying worker, so an outage delays mail instead of dropping
-- it.
--
-- failed_at marks a message that exhausted its attempts, which keeps it visible
-- in the admin UI for a manual retry rather than deleting it.

CREATE TABLE outbound_mail (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    recipient       TEXT    NOT NULL,
    subject         TEXT    NOT NULL,
    body            TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    sent_at         INTEGER,
    failed_at       INTEGER,
    last_error      TEXT    NOT NULL DEFAULT ''
);

-- The worker's query: unsent, unfailed messages that are due.
CREATE INDEX idx_outbound_mail_due ON outbound_mail (sent_at, failed_at, next_attempt_at);

-- Housekeeping of delivered messages.
CREATE INDEX idx_outbound_mail_sent ON outbound_mail (sent_at);
