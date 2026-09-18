-- A record per distinct piece of content, with a reference count.
--
-- Content-addressed storage already meant identical uploads share their bytes.
-- What it did not have was a cheap way to answer "is anything still using
-- this?", which was a COUNT over the file rows naming the bytes, run once per
-- key on every deletion. This replaces that with a number the database keeps
-- itself.
--
-- The count is maintained by triggers rather than by callers, which is the part
-- that makes it safe: SQLite fires them for cascade deletes too, so removing an
-- account and everything it owned decrements every count it should. A counter
-- maintained by application code would leak here, because the file rows
-- disappear inside the database rather than through a statement this code
-- controls.

CREATE TABLE blobs (
    sha256      TEXT    PRIMARY KEY,
    size        INTEGER NOT NULL,
    object_key  TEXT    NOT NULL,
    thumb_key   TEXT    NOT NULL DEFAULT '',
    preview_key TEXT    NOT NULL DEFAULT '',
    refcount    INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);

-- The sweep for content nothing refers to any more.
CREATE INDEX idx_blobs_refcount ON blobs (refcount);

-- One blob per distinct hash. Where rows predating de-duplication share a hash
-- but not a key set, one set wins and the others' objects are left behind: the
-- content is identical, so every file still serves the right bytes, but those
-- copies are not reclaimed and are no longer referenced by anything.
INSERT INTO blobs (sha256, size, object_key, thumb_key, preview_key, refcount, created_at)
SELECT f.sha256, MAX(f.size), f.object_key, f.thumb_key, f.preview_key,
       COUNT(*), MIN(f.created_at)
FROM files f
GROUP BY f.sha256;

CREATE TRIGGER blobs_refcount_on_insert AFTER INSERT ON files
BEGIN
    UPDATE blobs SET refcount = refcount + 1 WHERE sha256 = NEW.sha256;
END;

CREATE TRIGGER blobs_refcount_on_delete AFTER DELETE ON files
BEGIN
    UPDATE blobs SET refcount = refcount - 1 WHERE sha256 = OLD.sha256;
END;

-- The keys live on the blob now. A copy on every file row would be a second
-- source of truth, and the two could disagree.
DROP INDEX idx_files_object_key;
DROP INDEX idx_files_thumb_key;
DROP INDEX idx_files_preview_key;

ALTER TABLE files DROP COLUMN object_key;
ALTER TABLE files DROP COLUMN thumb_key;
ALTER TABLE files DROP COLUMN preview_key;
