-- Reports, and a record of what moderators did about them.
--
-- A moderator rank with no way for anybody to raise a problem just means the
-- moderator has to notice it themselves. This is the other half: members can
-- report a file or an album, the reports land in a queue, and every removal
-- records who did it, when, and why.

CREATE TABLE reports (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    target_kind TEXT    NOT NULL,
    -- A file id, or an album slug. Albums keep their slug across a rename, so
    -- it stays a usable reference.
    target_id   TEXT    NOT NULL,
    -- Nullable and cleared rather than cascading: the report is evidence, and it
    -- should outlive the account that raised it.
    reporter_id INTEGER REFERENCES users (id) ON DELETE SET NULL,
    -- A snapshot of the name, so a deleted account does not turn the queue into
    -- a list of numbers.
    reporter    TEXT    NOT NULL DEFAULT '',
    reason      TEXT    NOT NULL,
    note        TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'open',
    created_at  INTEGER NOT NULL,
    resolved_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    resolved_at INTEGER,
    resolution  TEXT    NOT NULL DEFAULT ''
);

-- The queue reads this constantly: open reports, oldest first, because a
-- queue is worked from the front.
CREATE INDEX idx_reports_status ON reports (status, created_at);

-- One open report per account per target. Reporting the same thing again is not
-- more persuasive, and a queue that fills with duplicates is a queue nobody
-- works. Resolving or dismissing a report lifts this, so a second complaint
-- after a dismissal is allowed.
CREATE UNIQUE INDEX idx_reports_one_open
    ON reports (reporter_id, target_kind, target_id) WHERE status = 'open';

CREATE TABLE moderation_log (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    -- Nullable for the same reason, with the name kept alongside: deleting a
    -- moderator must not erase the fact that they removed something.
    actor_id     INTEGER REFERENCES users (id) ON DELETE SET NULL,
    actor_name   TEXT    NOT NULL DEFAULT '',
    action       TEXT    NOT NULL,
    target_kind  TEXT    NOT NULL DEFAULT '',
    target_id    TEXT    NOT NULL DEFAULT '',
    -- The name of the thing at the moment it was acted on. The target is often
    -- gone by the time anybody reads this, which is exactly when it matters.
    target_label TEXT    NOT NULL DEFAULT '',
    reason       TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL
);

CREATE INDEX idx_moderation_log_created ON moderation_log (created_at DESC);
