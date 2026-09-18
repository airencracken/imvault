-- A role, in place of a single administrator flag.
--
-- Two roles were enough for one person's instance: you, and everybody else.
-- A group needs a third. Somebody has to be able to remove a bad upload and
-- work a report queue without also being handed the ability to change policy,
-- manage accounts, or promote themselves.
--
-- Administrator is the only role that implies the others, so it is the only one
-- that could be expressed as a flag. The migration maps the flag onto the role
-- rather than the reverse, so nothing gains power: an existing administrator
-- stays one, and everybody else becomes an ordinary member.

ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'member';
UPDATE users SET role = 'admin' WHERE is_admin = 1;
ALTER TABLE users DROP COLUMN is_admin;

-- The account list groups and filters by role.
CREATE INDEX idx_users_role ON users (role);
