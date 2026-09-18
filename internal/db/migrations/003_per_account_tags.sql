-- Tags become a per-account namespace.
--
-- Previously a tag was global: "beach" was one row shared by everybody on the
-- instance, and the unique constraint on slug and name was instance-wide. Now
-- each account owns its own tag rows, so two people can both use "beach"
-- without sharing a label, a count or a lifetime.
--
-- Existing tags are split rather than discarded: each one is copied into the
-- namespace of every account whose files carried it, and the links are
-- repointed at the relevant copy.

ALTER TABLE file_tags RENAME TO file_tags_old;
ALTER TABLE tags RENAME TO tags_old;

CREATE TABLE tags (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL COLLATE NOCASE,
    slug       TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (user_id, name),
    UNIQUE (user_id, slug)
);

CREATE TABLE file_tags (
    file_id TEXT    NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (file_id, tag_id)
);

-- One copy of the tag per account that was using it.
INSERT INTO tags (user_id, name, slug, created_at)
SELECT DISTINCT f.user_id, t.name, t.slug, CAST(strftime('%s', 'now') AS INTEGER)
FROM tags_old t
JOIN file_tags_old ft ON ft.tag_id = t.id
JOIN files f ON f.id = ft.file_id
WHERE f.user_id IS NOT NULL;

-- Repoint every link at the copy belonging to the file's owner.
--
-- Links on anonymous uploads are dropped: those files have no account, and so
-- no namespace for a tag to live in. They could not be tagged again anyway.
INSERT OR IGNORE INTO file_tags (file_id, tag_id)
SELECT ft.file_id, own.id
FROM file_tags_old ft
JOIN files f ON f.id = ft.file_id
JOIN tags_old t ON t.id = ft.tag_id
JOIN tags own ON own.user_id = f.user_id AND own.name = t.name
WHERE f.user_id IS NOT NULL;

DROP TABLE file_tags_old;
DROP TABLE tags_old;

-- Created last: the index that came with the old table shares this name and
-- only disappears with it.
CREATE INDEX idx_file_tags_tag ON file_tags (tag_id);
