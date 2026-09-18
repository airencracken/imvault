-- Invitations: a code that admits one account, for instances whose
-- registration is closed or restricted.
--
-- Until now an instance was open to anybody who found it, or closed to
-- everybody, and the only way to add a person to a closed one was to hand-edit
-- the database. That is why the "Group" profile had to leave signup open. An
-- invitation is the missing middle: a deliberate grant, revocable, limited in
-- uses and time, that an administrator hands to somebody specific.
--
-- The code itself is not stored. The prefix is kept in the clear so redemption
-- touches one row, and the rest is a digest, exactly as API keys are kept, so a
-- database leak does not hand out accounts.

CREATE TABLE invites (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    prefix     TEXT    NOT NULL UNIQUE,
    code_hash  TEXT    NOT NULL,
    label      TEXT    NOT NULL DEFAULT '',
    -- Nullable, and set to null if the administrator who issued it is deleted:
    -- the invitation is still valid, it just loses its attribution.
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER,
    -- Zero means no limit, matching how quotas read zero.
    max_uses   INTEGER NOT NULL DEFAULT 1,
    uses       INTEGER NOT NULL DEFAULT 0,
    revoked_at INTEGER
);
