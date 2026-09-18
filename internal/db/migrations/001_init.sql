-- Core schema for imvault.
--
-- All timestamps are stored as INTEGER unix seconds (UTC) to keep comparisons
-- unambiguous and independent of SQLite driver time handling.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    email         TEXT    NOT NULL DEFAULT '' COLLATE NOCASE,
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
    token_hash TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE INDEX idx_sessions_expires ON sessions (expires_at);

CREATE TABLE files (
    id            TEXT    PRIMARY KEY,
    user_id       INTEGER REFERENCES users (id) ON DELETE CASCADE,
    original_name TEXT    NOT NULL,
    ext           TEXT    NOT NULL,
    mime          TEXT    NOT NULL,
    size          INTEGER NOT NULL,
    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    sha256        TEXT    NOT NULL,
    object_key    TEXT    NOT NULL,
    thumb_key     TEXT    NOT NULL DEFAULT '',
    preview_key   TEXT    NOT NULL DEFAULT '',
    is_public     INTEGER NOT NULL DEFAULT 0,
    views         INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER
);

CREATE INDEX idx_files_user_created ON files (user_id, created_at DESC);
CREATE INDEX idx_files_created ON files (created_at DESC);
CREATE INDEX idx_files_expires ON files (expires_at);
CREATE INDEX idx_files_sha256 ON files (sha256);

CREATE TABLE albums (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title       TEXT    NOT NULL,
    slug        TEXT    NOT NULL UNIQUE,
    description TEXT    NOT NULL DEFAULT '',
    is_public   INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);

CREATE INDEX idx_albums_user ON albums (user_id, created_at DESC);

CREATE TABLE album_files (
    album_id INTEGER NOT NULL REFERENCES albums (id) ON DELETE CASCADE,
    file_id  TEXT    NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    added_at INTEGER NOT NULL,
    PRIMARY KEY (album_id, file_id)
);

CREATE INDEX idx_album_files_file ON album_files (file_id);

CREATE TABLE tags (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    slug TEXT    NOT NULL UNIQUE
);

CREATE TABLE file_tags (
    file_id TEXT    NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (file_id, tag_id)
);

CREATE INDEX idx_file_tags_tag ON file_tags (tag_id);
