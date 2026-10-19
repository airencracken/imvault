-- Camera wall time: EXIF often has no timezone. Empty means unknown.
ALTER TABLE blobs ADD COLUMN taken_at TEXT NOT NULL DEFAULT '';
CREATE INDEX blobs_taken ON blobs(taken_at DESC, sha256);
