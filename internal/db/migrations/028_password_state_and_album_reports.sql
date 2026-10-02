-- Whether an account has a password anybody knows.
--
-- An account made through an identity provider is given a random password, so
-- that "no password" can never be misread as "any password". Nobody knows that
-- password, though, and every setting guarded by "enter your current password"
-- was out of reach for such an account, while disconnecting its provider locked
-- it out for good. This records the difference so those paths can ask for a
-- fresh provider sign-in instead.
--
-- Existing accounts keep a password unless they were plainly made by a provider:
-- an identity recorded in the same second as the account itself, which is what
-- the provider registration does in one transaction. Linking a provider to an
-- existing account always happens later, after a round trip to the provider.
ALTER TABLE users ADD COLUMN password_set INTEGER NOT NULL DEFAULT 1 CHECK (password_set IN (0, 1));

UPDATE users SET password_set = 0
WHERE EXISTS (
    SELECT 1 FROM user_identities i
    WHERE i.user_id = users.id
      AND i.created_at BETWEEN users.created_at AND users.created_at + 1
);

-- Any change to the hash is somebody choosing a password: a reset link, a
-- change from the settings page, or setting one for the first time. Keeping the
-- flag in step here means no path that writes a password can forget to.
CREATE TRIGGER users_password_becomes_set
AFTER UPDATE OF password_hash ON users
WHEN OLD.password_set = 0 AND NEW.password_hash IS NOT OLD.password_hash
BEGIN
    UPDATE users SET password_set = 1 WHERE id = NEW.id;
END;

-- Album reports name the album by id rather than by slug.
--
-- A slug comes from the title and is free again the moment its album is
-- deleted, so a report about one album came to point at the next album given
-- the same title, and resolving the report removed that one instead. Ids are
-- never reused. A report whose album is already gone cannot be resolved to an
-- id, so it is marked as such and can only ever read as gone.
--
-- One statement, so each row is judged by its slug as it stood: a slug that
-- happens to be a number must not be mistaken for an id already converted.
UPDATE reports
SET target_id = COALESCE(
    (SELECT CAST(a.id AS TEXT) FROM albums a WHERE a.slug = reports.target_id),
    'gone:' || target_id)
WHERE target_kind = 'album';
