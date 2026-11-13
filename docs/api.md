# API

Use the API to upload and organise your own files from scripts and tools such
as ShareX.

Create a key under **API keys** in your account settings. It is shown once; if
you lose it, revoke it and create another.

```bash
export IMVAULT_KEY="imv_lxy8k3m9q0ab_2f8c1d9e4a7b6c0d3e5f8192a4b6c8d0"

# Who am I?
curl -H "Authorization: Bearer $IMVAULT_KEY" https://img.example.com/api/v1/me

# Upload
curl -H "Authorization: Bearer $IMVAULT_KEY" \
     -F "files=@photo.jpg" \
     -F "visibility=public" \
     https://img.example.com/api/v1/upload
```

`X-API-Key: <key>` works anywhere `Authorization: Bearer` does.

A key acts as its account. It works for `/api/v1/...` and for media downloads
(`GET` and `HEAD` on `/f/{id}/raw`, `/thumb` and `/preview`). Account settings
and administration need a browser session. Request bodies other than uploads
are limited to 1 MiB; uploads use the instance's upload limits.

Write endpoints accept JSON or ordinary form encoding.

**Upload options** (multipart form fields)

| Field | Meaning |
| --- | --- |
| `files` | One or more files. `file`, `image`, and `uploads` are accepted as aliases |
| `visibility` | `public`, `members`, or `private`. Defaults to the instance default |
| `metadata` | `shown`, `inherit`, or `hidden`. Defaults to `inherit`, which follows visibility |
| `location` | `shown`, `inherit`, or `hidden`. Defaults to `inherit`, which follows EXIF independently of an explicit location choice |
| `public` | The older boolean. `1` means `visibility=public`, `0` means `private` |
| `album` | Album slug or id to add the uploads to |
| `tags` | Comma-separated tag names to apply to the uploads |

The file response carries a `details` object with whatever the file said about
itself — `taken`, `camera`, `lens`, `exposure`, `aperture`, `iso`, `focal`,
`software`, and the identifying `artist`, `copyright`, `serial`, `latitude`,
`longitude`, and `altitude`. It is omitted when there is nothing to report. The
API is not masked: it returns an account's own files to that account.

Visibility is `public` (anyone with the link), `members` (any signed-in
account) or `private` (you, plus administrators and moderators). An upload
without a level gets the instance default. Anonymous uploads are always public.
Prefer `visibility` over the older `public` field.

**Response formats**

- Default: JSON with `files[]` (id, name, description, mime, kind, dimensions,
  tags, `page_url`, `raw_url`, `thumb_url`, `url`) and any per-file `errors[]`.
- `?format=text` or `Accept: text/plain`: one URL per line, for ShareX-style
  uploaders.

A ShareX custom uploader:

| Setting | Value |
| --- | --- |
| Request URL | `https://img.example.com/api/v1/upload?format=text` |
| Request method | `POST` |
| Body | `Form data (multipart/form-data)` |
| File form name | `files` |
| Headers | `Authorization: Bearer <key>` |

## File descriptions

File JSON includes `description`, empty until set. Set or clear it with
`PATCH /api/v1/files/{id}`:

```bash
curl -X PATCH -H "Authorization: Bearer $IMVAULT_KEY" \
     -H 'Content-Type: application/json' \
     -d '{"description":"The summer picnic.\nSomebody brought three cakes."}' \
     https://img.example.com/api/v1/files/abc123
```

Descriptions are plain text, limited to 1,000 Unicode characters. Surrounding
whitespace is trimmed and CRLF line endings become LF. Send `"description":""`
to clear it; omitting the field preserves it. `null` and non-string values are
errors. Ordinary form encoding works too. Descriptions follow file visibility,
independently of the camera metadata policy.

You can send `description`, `visibility`, `metadata`, and `location` together. All supplied
fields are validated and saved together; an invalid value leaves the file
unchanged.

## Location sharing

File and album JSON include `metadata` and `location` policy strings.
Set `location` on upload, album creation, or a file/album PATCH. Accepted values
are `shown`, `hidden`, and `inherit` (**Follow EXIF**, the upgrade default).
Omitting it on PATCH keeps the existing choice. Empty strings, `null`, and
non-string values are rejected before any changes are saved.

For example, `{"metadata":"shown","location":"hidden"}` shares camera details
without GPS. `{"metadata":"hidden","location":"shown"}` shares GPS without
camera details. Albums may restrict either category, but cannot reveal details
hidden by a file or another album. See [media handling](media.md#metadata) for
page, download, and format behavior.

## Albums and tags

Address albums by slug or numeric ID. An album's `access` decides who can add
their own files to it:

| `access` | Who can add |
| --- | --- |
| `owner` | The creator and administrators (default) |
| `members` | Any signed-in account that can see the album |

Contributors add only their own files and can take them back out. Only the
owner can remove someone else's file. A shared album must be visible to
members.

```bash
# Create an album
curl -H "Authorization: Bearer $IMVAULT_KEY" -H 'Content-Type: application/json' \
     -d '{"title":"Summer 2026","description":"Warm ones","visibility":"public"}' \
     https://img.example.com/api/v1/albums

# One anybody on the instance may add their own images to
curl -H "Authorization: Bearer $IMVAULT_KEY" -H 'Content-Type: application/json' \
     -d '{"title":"Raid Night","visibility":"members","access":"members"}' \
     https://img.example.com/api/v1/albums

# Add uploads to it (or seed it at creation time with a "files" array)
curl -H "Authorization: Bearer $IMVAULT_KEY" -H 'Content-Type: application/json' \
     -d '{"files":["abc123","def456"]}' \
     https://img.example.com/api/v1/albums/summer-2026/files

# PATCH changes only the fields you send
curl -X PATCH -H "Authorization: Bearer $IMVAULT_KEY" \
     -H 'Content-Type: application/json' -d '{"visibility":"private"}' \
     https://img.example.com/api/v1/albums/summer-2026

# Tag a file; the response is the file's tag list
curl -H "Authorization: Bearer $IMVAULT_KEY" -H 'Content-Type: application/json' \
     -d '{"name":"beach"}' \
     https://img.example.com/api/v1/files/abc123/tags

# Detach by id, slug or name
curl -X DELETE -H "Authorization: Bearer $IMVAULT_KEY" \
     https://img.example.com/api/v1/files/abc123/tags/beach

# Add several tags to several files, keeping existing tags
curl -H "Authorization: Bearer $IMVAULT_KEY" -H 'Content-Type: application/json' \
     -d '{"files":["abc123","def456"],"tags":["Holiday","Family"]}' \
     https://img.example.com/api/v1/files/tags

# Find things
curl -H "Authorization: Bearer $IMVAULT_KEY" \
     'https://img.example.com/api/v1/files?tag=beach&kind=image&visibility=public'
```

Bulk tagging takes 1 to 500 file IDs and 1 to 50 tag names of up to 48
characters, as JSON arrays or repeated `files` and `tags` form fields. It
returns `{"tagged":2}`, counting each selected file once, and is safe to
repeat. Every file must be yours, even for administrators. Invalid input
returns 400; a missing, expired or unowned file returns 404 and nothing
changes.

Tags on anonymous uploads have an empty `username` in tag JSON, and live at
`/tags/~/<slug>` in the web pages.

## Listing files

`GET /api/v1/files` lists your uploads. It returns `files`, `total`, `limit` and
`offset`, and accepts:

| Parameter | Values |
| --- | --- |
| `q` | Search text |
| `tag`, `album` | Filter by tag or album |
| `visibility` | `public`, `members` or `private` |
| `public` | Older filter: `1` for public, `0` for anything not public |
| `kind` | `image`, `animated` or `video` |
| `sort` | `uploaded` (default) or `taken`; newest first, undated files last for `taken` |
| `year` | Camera year, `0001` to `9999`, or `unknown` |
| `month` | `01` to `12`; needs a numeric `year` |
| `limit`, `offset` | Paging; `limit` defaults to 50 |

Invalid combinations return 400. Camera dates are wall-clock times with no
assumed timezone, and missing dates are never filled with the upload time.

## Library actions

`POST /api/v1/library/album` adds `files` (an array of file IDs) to `album` (an
album ID). To create an album and add the files in one step, send
`"album":"new"` and an `album_title` of 1 to 120 characters. New albums are
private. If anything fails, no album is created.

`POST /api/v1/library/visibility` sets `visibility` (`public`, `members` or
`private`) on `files`.

Both return `{"updated": N}`, counting repeated IDs once. Every file must be
yours, even for administrators, with at most 500 per request, and you must be
allowed to add to the album. Adding to an album keeps each file's visibility
and other albums. Invalid values return 400; an unavailable file or album
returns 404. A failed request changes nothing.

## Photo rotation

File responses include `rotation` (clockwise degrees: 0, 90, 180 or 270) and
`preview_url`. Thumbnails and previews show the rotation; `raw_url` always
serves the original. When rotation is not 0, `/f/{id}/rotated` serves a
full-resolution rotated PNG without metadata, under the file's usual access
rules. Account exports keep the original and record its rotation.

## Trash

`DELETE /api/v1/files/{id}` moves your upload to Trash and returns
`{"status":"trashed","id":"..."}`. The file and its media URLs then return 404.
When staff delete someone else's upload, it is deleted permanently and the
status is `"deleted"`.

| Method | Path | Result |
| --- | --- | --- |
| `GET` | `/api/v1/trash` | Your trashed files; `limit` (1 to 200, default 50) and `offset` |
| `POST` | `/api/v1/trash/{id}/restore` | `{"status":"restored","id":"..."}` |
| `DELETE` | `/api/v1/trash/{id}` | Permanent deletion: `{"status":"deleted","id":"..."}` |

The list returns `files`, `total`, `limit` and `offset`. Each file has `id`,
`name`, `size`, `trashed_at` and `purge_after`, as UTC RFC 3339 strings. Only
the owner's key works here; administrators cannot restore someone else's files.

You can restore a file for 30 days. After that, restoring returns 404, but
permanent deletion still works until cleanup removes the file. A missing,
foreign or untrashed file returns 404. Restoring brings back the file's sharing
settings and any albums that still exist. Trashed files count toward your quota
until purged, and account exports include them with `trashed_at`. Backups may
still hold files after permanent deletion.

## Album previews

`GET /api/v1/albums/{ref}/preview` summarises one of your public albums for
Witmoot. See [Album discussions](discussions.md).

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/me` | Identify the calling key |
| `POST` | `/api/v1/upload` | Upload one or more files |
| `GET` | `/api/v1/files` | List and filter your uploads |
| `GET` | `/api/v1/files/{id}` | One file |
| `PATCH` | `/api/v1/files/{id}` | Change `description`, `visibility`, `metadata`, `location`, or the older `public` |
| `DELETE` | `/api/v1/files/{id}` | Move a file to Trash |
| `POST` | `/api/v1/files/{id}/tags` | Attach a tag |
| `POST` | `/api/v1/files/tags` | Add tags to several of your files at once |
| `DELETE` | `/api/v1/files/{id}/tags/{ref}` | Detach a tag by id, slug or name |
| `POST` | `/api/v1/library/album` | Add files to an album, or to a new one |
| `POST` | `/api/v1/library/visibility` | Change visibility of several files |
| `GET` | `/api/v1/trash` | List your Trash |
| `POST` | `/api/v1/trash/{id}/restore` | Restore a file from Trash |
| `DELETE` | `/api/v1/trash/{id}` | Delete a trashed file permanently |
| `GET` | `/api/v1/tags` | List tags |
| `GET` | `/api/v1/albums` | List your albums |
| `POST` | `/api/v1/albums` | Create an album, optionally with `files` |
| `GET` | `/api/v1/albums/{ref}/preview` | Summary of one of your public albums |
| `GET` | `/api/v1/albums/{ref}` | An album with its files |
| `PATCH` | `/api/v1/albums/{ref}` | Update title, description, visibility, `access`, `metadata`, or `location` |
| `DELETE` | `/api/v1/albums/{ref}` | Delete an album, keeping its files |
| `POST` | `/api/v1/albums/{ref}/files` | Add files to an album |
| `DELETE` | `/api/v1/albums/{ref}/files/{id}` | Remove a file from an album |

Another account's file or album returns 404, never 403.
