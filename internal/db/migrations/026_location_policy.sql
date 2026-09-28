-- Follow EXIF preserves the location policy of every existing file and album.
ALTER TABLE files ADD COLUMN location TEXT NOT NULL DEFAULT 'inherit'
    CHECK (location IN ('inherit', 'shown', 'hidden'));
ALTER TABLE albums ADD COLUMN location TEXT NOT NULL DEFAULT 'inherit'
    CHECK (location IN ('inherit', 'shown', 'hidden'));
ALTER TABLE blobs ADD COLUMN camera_key TEXT NOT NULL DEFAULT '';
ALTER TABLE blobs ADD COLUMN location_key TEXT NOT NULL DEFAULT '';
