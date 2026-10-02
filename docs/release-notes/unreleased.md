# Unreleased

Changes since 0.11.1 that operators should know about. Fold this into the next
version's release notes.

## Upgrade notes

- **Everyone is signed out once.** Over HTTPS, the session and CSRF cookies are
  now named `__Host-imvault_session` and `__Host-imvault_csrf`, so a sibling
  subdomain or a plain-HTTP page on the same host can no longer plant them.
  Cookies under the old names are ignored on HTTPS, so existing sessions end at
  the upgrade and people sign in again. Plain-HTTP instances, such as local
  development, keep the old names and their sessions.
- **An older binary will not start against a newer database.** A binary that
  finds migrations in the database it does not have now refuses to start, and
  so does every maintenance command, with a message naming them. Earlier
  versions carried on silently. To go back a version, restore the backup taken
  before the upgrade. See
  [going back to an older binary](../operations.md#going-back-to-an-older-binary).
- **`imvault backup` no longer migrates.** It runs only against a database at
  its own binary's schema, so a backup taken before an upgrade is a copy of the
  database as it was. Take it with the version that last ran the database.
  `imvault restore` refuses a backup taken by a newer Imvault.
