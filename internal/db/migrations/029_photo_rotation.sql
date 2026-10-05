ALTER TABLE files ADD COLUMN rotation INTEGER NOT NULL DEFAULT 0
 CHECK(rotation IN (0, 90, 180, 270) AND (rotation = 0 OR kind = 'image'));
