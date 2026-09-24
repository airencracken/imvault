ALTER TABLE users ADD COLUMN can_invite INTEGER NOT NULL DEFAULT 0 CHECK(can_invite IN (0, 1));
ALTER TABLE users ADD COLUMN invited_by INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE users ADD COLUMN invited_by_username TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN invitation_id INTEGER REFERENCES invites(id) ON DELETE SET NULL;
CREATE INDEX users_invited_by ON users(invited_by, id);
CREATE INDEX users_invitation ON users(invitation_id, id);
