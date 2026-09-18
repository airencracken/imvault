-- Media kinds (still image / animated / video) and API keys.

ALTER TABLE files ADD COLUMN kind        TEXT    NOT NULL DEFAULT 'image';
ALTER TABLE files ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE files ADD COLUMN frame_count INTEGER NOT NULL DEFAULT 0;

CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    prefix       TEXT    NOT NULL UNIQUE,
    key_hash     TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    expires_at   INTEGER
);

CREATE INDEX idx_api_keys_user ON api_keys (user_id, created_at DESC);
CREATE INDEX idx_api_keys_expires ON api_keys (expires_at);
