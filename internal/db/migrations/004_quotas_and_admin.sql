-- Per-account storage quotas and the flags the admin UI needs.
--
-- quota_bytes of 0 means unlimited, which is also the state every existing
-- account is left in: adding a cap to accounts that were created before quotas
-- existed would silently break them.
--
-- storage_used is a denormalised running total of the original bytes each
-- account owns. It is reserved atomically on upload and released on delete, and
-- backfilled here from what is already stored.

ALTER TABLE users ADD COLUMN quota_bytes  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN storage_used INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN disabled     INTEGER NOT NULL DEFAULT 0;

UPDATE users SET storage_used = (
    SELECT COALESCE(SUM(f.size), 0) FROM files f WHERE f.user_id = users.id
);

CREATE INDEX idx_files_user_size ON files (user_id, size);
