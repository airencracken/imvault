-- Password reset and email verification tokens.
--
-- Tokens are stored as a SHA-256 digest, exactly like sessions and API keys, so
-- a database leak does not hand over working reset links. Only one row per
-- digest, and consumption is recorded rather than deleted so a replay can be
-- told apart from a bad token.

CREATE TABLE auth_tokens (
    token_hash TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
);

CREATE INDEX idx_auth_tokens_user ON auth_tokens (user_id, purpose);
CREATE INDEX idx_auth_tokens_expires ON auth_tokens (expires_at);

ALTER TABLE users ADD COLUMN email_verified INTEGER NOT NULL DEFAULT 0;
