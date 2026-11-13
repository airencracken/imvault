-- Camera details and location become two plain choices on each file.
--
-- Each file keeps what other people could see before this migration. The old
-- settings resolve the way serving resolved them, and the strictest answer
-- wins:
--
--   camera details  the file's EXIF setting, where "inherit" means hidden on
--                   public files and shown otherwise, then any album holding
--                   the file with EXIF hidden.
--   location        the file's location setting, where "inherit" means the
--                   file's resolved EXIF answer, then any album holding the
--                   file with location hidden, or with location following an
--                   EXIF setting of hidden.
--
-- Albums lose both settings. Nothing becomes more visible than it was.

ALTER TABLE files ADD COLUMN show_camera INTEGER NOT NULL DEFAULT 1
    CHECK (show_camera IN (0, 1));
ALTER TABLE files ADD COLUMN show_location INTEGER NOT NULL DEFAULT 0
    CHECK (show_location IN (0, 1));

UPDATE files SET
    show_camera = CASE
        WHEN lower(trim(metadata)) = 'hidden' THEN 0
        WHEN lower(trim(metadata)) = 'shown' THEN 1
        WHEN lower(trim(visibility)) = 'public' THEN 0
        ELSE 1
    END,
    show_location = CASE
        WHEN lower(trim(location)) = 'hidden' THEN 0
        WHEN lower(trim(location)) = 'shown' THEN 1
        WHEN lower(trim(metadata)) = 'hidden' THEN 0
        WHEN lower(trim(metadata)) = 'shown' THEN 1
        WHEN lower(trim(visibility)) = 'public' THEN 0
        ELSE 1
    END;

UPDATE files SET show_camera = 0
WHERE show_camera = 1 AND EXISTS (
    SELECT 1 FROM album_files af JOIN albums a ON a.id = af.album_id
    WHERE af.file_id = files.id AND lower(trim(a.metadata)) = 'hidden');

UPDATE files SET show_location = 0
WHERE show_location = 1 AND EXISTS (
    SELECT 1 FROM album_files af JOIN albums a ON a.id = af.album_id
    WHERE af.file_id = files.id
      AND (lower(trim(a.location)) = 'hidden' OR (lower(trim(a.location)) = 'inherit' AND lower(trim(a.metadata)) = 'hidden')));

ALTER TABLE files DROP COLUMN metadata;
ALTER TABLE files DROP COLUMN location;
ALTER TABLE albums DROP COLUMN metadata;
ALTER TABLE albums DROP COLUMN location;
