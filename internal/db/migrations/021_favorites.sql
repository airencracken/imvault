-- Favorites are private bookmarks; they do not grant access to a file.
CREATE TABLE favorites (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    file_id    TEXT    NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, file_id)
);

CREATE INDEX idx_favorites_user_created ON favorites (user_id, created_at DESC, file_id DESC);
CREATE INDEX idx_favorites_file ON favorites (file_id);
