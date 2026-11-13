# Organizing your library

## Browse by date taken

Open **Your gallery** and choose **Date taken** under **Sort by**. Photos with
camera dates come first, newest at the top, then files without one. Pick a year,
and a month if you like, or **Unknown date** to find undated files. Search works
with these controls. **Date uploaded** is the default.

Dates come from the camera's recorded local time. Imvault does not guess a
timezone or fill in the upload date. Screenshots, animations and clips often have
no capture date, and each gallery card shows which date it is using.

If an older photo is missing its date, [refresh its
details](media.md#refreshing-older-photo-details).

Date sorting and filtering cover only your own gallery and your own files in the
API. Nobody can search another person's hidden camera dates through shared
albums or the recent feed.

## Act on several photos

Tick the checkboxes on gallery cards, then use **Organize selected photos**:

- **Add tags** keeps existing tags.
- **Add to album** keeps other album memberships. You can pick your own albums
  and shared albums you contribute to. Adding never changes a file's visibility.
- **Change sharing** sets every selected file to public, members-only or private.
  Existing links follow the new setting. EXIF and location settings still apply.

Selection covers the current page. With JavaScript you also get **Select all on
this page**, **Clear selection** and a count. Everything else works without it.
If any file in the batch can't be changed, nothing changes. You can bulk-organize
only your own files, administrators included.

## Trash and restore

On a file you own, choose **Move to Trash**. Its page, downloads and shared links
stop working at once, and it leaves galleries, albums, favorites and the recent
feed. Copies people already downloaded are out of reach.

Choose **Trash** beside the gallery title to see what you removed. **Restore**
works for 30 days and brings back the sharing setting, tags, favorites and
album memberships. Albums deleted in the meantime stay deleted. Nobody else,
staff included, can see your Trash or restore for you.

Trashed files still count toward your storage. **Permanently delete** frees
your quota at once, after a confirmation. After 30 days, files are purged
automatically.

Staff removals are permanent, and moving a reported file to Trash does not stop
staff from removing it. Anonymous uploads expire as before. Deleting your
account deletes your Trash too.

Account exports include trashed files, marked with `trashed_at`. Instance backups
include Trash, so a backup can hold a file after you permanently delete it. See
[backups](operations.md#backups-and-restore-drills).
