-- Two-factor authentication, and the pieces self-service deletion needs.
--
-- totp_secret holds the shared secret, encrypted with the key beside the
-- database: unlike a session or an API key, it has to be readable again in
-- order to check a code, so it cannot be a digest.
--
-- A secret is stored as soon as enrolment begins, with totp_enabled still 0, so
-- that a half-finished enrolment is not treated as protection that exists.
--
-- totp_last_step records the last time step accepted, so a code cannot be
-- replayed for the rest of its window.

ALTER TABLE users ADD COLUMN totp_secret    TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_enabled   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;

-- Recovery codes are the way back in when the authenticator is gone. They are
-- stored as digests, like every other credential: the server never needs one
-- back in the clear after it has been shown.
CREATE TABLE recovery_codes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    used_at    INTEGER
);

CREATE INDEX idx_recovery_codes_user ON recovery_codes (user_id);
CREATE INDEX idx_recovery_codes_hash ON recovery_codes (user_id, code_hash);
