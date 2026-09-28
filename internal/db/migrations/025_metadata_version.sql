-- Existing originals are refreshed on their next photo-page or API detail read.
-- Empty metadata is versioned too, so plain images are not parsed repeatedly.
ALTER TABLE blobs ADD COLUMN details_version INTEGER NOT NULL DEFAULT 0 CHECK (details_version >= 0);
