-- Links between a local account and one at an identity provider.
--
-- The subject is what identifies somebody. An email can be reassigned by the
-- provider and a username can change, but a subject is stable and unique within
-- an issuer, which is exactly what a durable link needs. The email is kept only
-- so the account page can show which address the provider asserted.
--
-- One account may have several identities, which is why this is a table rather
-- than a column: an instance with one provider configured today may have two
-- tomorrow, and a person who changes providers should not lose their uploads.

CREATE TABLE user_identities (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    issuer     TEXT    NOT NULL,
    subject    TEXT    NOT NULL,
    email      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    last_login INTEGER,
    -- One identity per issuer per account, enforced by the database rather than
    -- by a check that two callbacks could race past.
    UNIQUE (issuer, subject)
);

CREATE INDEX idx_user_identities_user ON user_identities (user_id);
