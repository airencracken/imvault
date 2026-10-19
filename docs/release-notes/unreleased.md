# Unreleased

Imvault's gallery can sort by camera date and filter by year or month, with an
explicit Unknown date group. Existing cached dates are indexed during upgrade,
and uploads and metadata refreshes maintain the index. Camera wall time is
preserved without guessing a timezone or replacing missing dates with upload
time. Date browsing stays in the owner's library.

The gallery's selection controls now add files to albums and change visibility
alongside bulk tagging. Ownership and album permissions are checked in the same
transaction as the batch; an invalid selection or failed write changes nothing.
Filters and pagination are retained after each action. All actions work without
JavaScript.

Owner deletion now moves uploads to private Trash for 30 days. Ordinary pages
and media links stop working immediately. Restoration preserves sharing, tags,
favorites and album membership. Files retain their quota and content references
until an explicit permanent deletion or expiry cleanup. Cleanup releases owned
storage as well as shared-content references, and rechecks expiry when deleting.
Account exports and backups include Trash. Staff removals remain permanent,
including when an owner has moved a reported upload to Trash first.

The API mirrors these operations. **Compatibility change:** deleting an owned
file with `DELETE /api/v1/files/{id}` now returns `status: "trashed"`. To reclaim
its quota immediately, follow with `DELETE /api/v1/trash/{id}`. Staff deletion
of somebody else's upload continues returning `status: "deleted"`.

Migrations **033_trash.sql** and **034_capture_dates.sql** add trash state and a
sortable capture-date cache. Capture-date backfill runs atomically with its
migration. Take a verified backup with the currently installed binary before
upgrading. Older binaries refuse the new schema; downgrading requires restoring
the matching pre-upgrade database and running its matching binary. Automatic
backups after startup do not replace that pre-upgrade backup.

See [organizing your library](../library.md) and the [API guide](../api.md).
