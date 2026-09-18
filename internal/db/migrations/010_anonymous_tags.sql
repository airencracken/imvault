-- Tags for anonymous uploads.
--
-- A tag belongs to a namespace. Until now that namespace was always an account,
-- and an anonymous upload had none, so it could not be tagged at all. The owner
-- column becomes nullable, and NULL means the one shared namespace that
-- anonymous uploads live in.
--
-- The table has to be rebuilt rather than altered: SQLite cannot make a NOT NULL
-- column nullable, and the uniqueness rules change with it. Tags are global to
-- the instance, so a plain UNIQUE over (user_id, name) will not do: SQLite
-- treats NULLs as distinct, which would allow any number of rows named "beach"
-- in the anonymous namespace. Two partial indexes express the two rules that
-- are actually wanted.

ALTER TABLE file_tags RENAME TO file_tags_old;
ALTER TABLE tags RENAME TO tags_old;

CREATE TABLE tags (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL COLLATE NOCASE,
    slug       TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX idx_tags_user_name ON tags (user_id, name) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX idx_tags_user_slug ON tags (user_id, slug) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX idx_tags_anon_name ON tags (name) WHERE user_id IS NULL;
CREATE UNIQUE INDEX idx_tags_anon_slug ON tags (slug) WHERE user_id IS NULL;

CREATE TABLE file_tags (
    file_id TEXT    NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (file_id, tag_id)
);

-- Ids are preserved, so every link keeps pointing at the tag it named.
INSERT INTO tags (id, user_id, name, slug, created_at)
SELECT id, user_id, name, slug, created_at FROM tags_old;

INSERT INTO file_tags (file_id, tag_id) SELECT file_id, tag_id FROM file_tags_old;

DROP TABLE file_tags_old;
DROP TABLE tags_old;

-- Created last: the index that came with the old table shares this name and
-- only disappears with it.
CREATE INDEX idx_file_tags_tag ON file_tags (tag_id);
