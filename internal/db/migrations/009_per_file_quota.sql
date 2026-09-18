-- A per-account ceiling on a single file, alongside the existing ceiling on the
-- account as a whole.
--
-- Zero means "no override": the account uses the instance defaults, which are
-- IMVAULT_MAX_UPLOAD_BYTES for images and IMVAULT_MAX_VIDEO_BYTES for clips. A
-- non-zero value replaces them for this account, so an administrator can raise
-- the limit for somebody trusted as well as lower it.

ALTER TABLE users ADD COLUMN max_file_bytes INTEGER NOT NULL DEFAULT 0;
