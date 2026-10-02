-- Invitations whose issuer has been deleted are revoked.
--
-- Deleting an account used to clear an invitation's issuer and leave the code
-- working, which meant deleting a disabled member brought their suspended codes
-- back. Deletion now revokes an account's codes, and redemption refuses a code
-- with no issuer. This marks the codes already in that state as what they now
-- are, so the invitation list says "revoked" rather than leaving an apparently
-- open code that silently refuses everybody.

UPDATE invites
SET revoked_at = CAST(strftime('%s', 'now') AS INTEGER)
WHERE created_by IS NULL AND revoked_at IS NULL;
