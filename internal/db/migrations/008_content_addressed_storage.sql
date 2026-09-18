-- Support for content-addressed storage.
--
-- Uploads are now stored under keys derived from their SHA-256, so identical
-- bytes land on the same object however many accounts upload them. Deleting a
-- file must therefore check whether anything else still points at the bytes
-- before removing them, which is what these indexes are for.
--
-- Nothing is backfilled: existing objects keep the keys recorded on their rows
-- and continue to work. Deduplication applies to uploads from here on.

CREATE INDEX idx_files_object_key  ON files (object_key);
CREATE INDEX idx_files_thumb_key   ON files (thumb_key);
CREATE INDEX idx_files_preview_key ON files (preview_key);
