# Organizing your library

## Browse by date taken

Open **Your gallery** and choose **Date taken** under **Sort by**. Photos with
camera dates appear newest first, followed by files without a readable date.
Choose a year and optionally a month to narrow the gallery, or **Unknown date**
to find undated files. Search works together with these controls. **Date uploaded**
is the default order.

Date taken uses the camera's recorded wall time. Imvault does not guess a
timezone or substitute an upload date when the camera date is absent. Screenshots,
animations and clips may have no readable capture date. The date shown on each
gallery card makes that distinction visible. Original camera dates are not edited.

Existing cached camera dates are indexed during upgrade; originals are not
opened or changed by that migration. Uploads and metadata refreshes keep the
index current. For older files whose cached metadata never recovered a date,
[refresh their metadata](media.md#refreshing-older-photo-details).

Date sorting and year/month filtering are confined to your own gallery and
owner-scoped file API. They do not make another person's hidden camera dates
searchable through shared albums or the recent feed.

## Act on several photos

Select the checkboxes on gallery cards, then use **Organize selected photos**:

- **Add tags** keeps any labels already attached.
- **Add to album** keeps other album memberships. The menu includes your albums
  and shared albums you can contribute to; adding a file never changes its own
  visibility.
- **Change sharing** sets every selected file to public, members-only or private.
  Making a file public affects its existing links too. Its EXIF and location
  policies still apply.

Selection covers the current page only. With JavaScript, **Select all on this
page**, **Clear selection** and a selection count help with larger batches.
Individual checkboxes and all actions also work without JavaScript. Each batch
is checked and written together: an unavailable file, changed album permissions
or failed write leaves the entire selection unchanged. Even administrators can
only bulk-organize their own files from their gallery.

## Trash and restore

On a file you own, choose **Move to Trash**. Its regular page, originals,
previews and shared links stop working immediately. It disappears from galleries,
albums, favorites and the recent feed. Previously downloaded or cached copies
cannot be recalled.

Choose **Trash** beside the gallery title to see your removed files and private
thumbnails. **Restore** is available for 30 days, returning the original sharing
settings, tags, favorites and album membership. Albums deleted in the meantime
cannot be brought back by restoring a file. Other members, including staff,
cannot use your Trash or restore files for you.

Files in Trash retain their content references and still count toward account
and instance storage limits. **Permanently delete** reclaims that file's quota
immediately; stored bytes are removed only when no other upload refers to them.
The browser asks you to confirm permanent deletion. The regular cleanup worker
purges files after 30 days; restoration is refused at the deadline even if
cleanup has not run yet.

Staff removals through moderation remain permanent. Moving a reported file to
Trash does not prevent staff from permanently removing it. Anonymous uploads
keep their existing expiry behavior. Account deletion is permanent and includes
Trash.

Account exports include trashed originals with a `trashed_at` marker and their
album memberships. Instance backups include Trash too; a backup can retain an
earlier copy after a file has been permanently deleted from the live instance.
See [backups and recovery](operations.md#backups-and-restore-drills).
