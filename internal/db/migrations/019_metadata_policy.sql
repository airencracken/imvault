-- Metadata visibility, as a setting of its own.
--
-- Until now a file's metadata travelled with its bytes: an original was stored
-- verbatim and served verbatim, so a phone photo's coordinates went wherever
-- the file went. Tying that to visibility alone would be a good default and a
-- poor ceiling, because people have opinions about individual pictures and
-- about whole collections, and those opinions do not always match the audience.
--
-- So metadata visibility is its own setting on a file and on an album, with
-- three values:
--
--   inherit  follow the file's visibility (the default, and what every
--            existing row means)
--   shown    keep the metadata however the file is shared
--   hidden   never serve the metadata
--
-- An album can only ever tighten: the effective policy is the most restrictive
-- of the file's own setting, its visibility's default, and every album it
-- belongs to. Shared albums are the reason. The person who owns an album and
-- the person who owns a file in it are not always the same, and an album must
-- not be able to make somebody else's file more exposed than they chose, nor to
-- loosen a choice they made. An album can add caution; only the owner can
-- remove it.

ALTER TABLE files  ADD COLUMN metadata TEXT NOT NULL DEFAULT 'inherit';
ALTER TABLE albums ADD COLUMN metadata TEXT NOT NULL DEFAULT 'inherit';

-- Where a metadata-free copy of this content lives.
--
-- It is recorded against the blob rather than the file because it is a function
-- of the bytes: two files holding the same content share one clean copy, and
-- whether either needs it is a policy that can change later. Kept alongside the
-- other keys so the deletion paths, which already work from the blob, remove it
-- without having to know what it is.
ALTER TABLE blobs ADD COLUMN clean_key TEXT NOT NULL DEFAULT '';
