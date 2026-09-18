-- Three-level visibility, in place of a public flag.
--
-- A boolean could say "me" and "everybody", but not "us", and "us" is the only
-- one of the three that a group has any use for. Without it, signing in grants
-- nothing over being a stranger, and an instance is a set of personal vaults
-- sharing a disk rather than a place.
--
-- Existing rows keep their meaning exactly: public stays public, and everything
-- else becomes private rather than members. Widening access during an upgrade
-- would be the wrong direction to guess in; an administrator can lift it by
-- setting the default, which governs new uploads from then on.

ALTER TABLE files ADD COLUMN visibility TEXT NOT NULL DEFAULT 'private';
UPDATE files SET visibility = 'public' WHERE is_public = 1;
ALTER TABLE files DROP COLUMN is_public;

ALTER TABLE albums ADD COLUMN visibility TEXT NOT NULL DEFAULT 'private';
UPDATE albums SET visibility = 'public' WHERE is_public = 1;
ALTER TABLE albums DROP COLUMN is_public;

-- Listings filter on this constantly.
CREATE INDEX idx_files_visibility  ON files (visibility);
CREATE INDEX idx_albums_visibility ON albums (visibility);
