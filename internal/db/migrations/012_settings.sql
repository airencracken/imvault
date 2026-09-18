-- Instance-wide policy that an administrator can change while running.
--
-- Everything was environment-only until now, which meant shortening the
-- anonymous retention window during an abuse report required editing a file and
-- restarting. Per-account policy has always been editable; these three are the
-- instance-wide equivalents.
--
-- A row exists only once an administrator has actually set something. Absence
-- means "use the environment", so a variable still applies to a setting nobody
-- has touched, and a fresh instance behaves exactly as its configuration says.

CREATE TABLE settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);
