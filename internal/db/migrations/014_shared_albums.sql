-- Shared albums: an album more than one account can put files into.
--
-- Until now an album had exactly one owner, so a clan's screenshots or a
-- family's holiday had to live in somebody's account. This adds the missing
-- primitive: whether accounts other than the creator may add their own files.
--
-- It defaults to the closed level for every existing row and every new one. An
-- album that quietly accepted anybody's files would be a surprise, and the
-- closed default is what an existing album already means.
--
-- Contributing still means adding your *own* files: this widens who may add,
-- never whose files may be added. Removing somebody else's file stays with the
-- album's owner, which is the part moderation will build on.

ALTER TABLE albums ADD COLUMN access TEXT NOT NULL DEFAULT 'owner';

-- Discovery: a member needs to find the albums they can contribute to without
-- being handed a link.
CREATE INDEX idx_albums_access ON albums (access, created_at DESC);
