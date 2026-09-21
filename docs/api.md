# API

The programmatic API, for scripts and upload tools.

Create a key under **API** in the navigation. It is displayed once and stored
only as a digest, so it cannot be recovered — revoke and re-create if lost.

```bash
export IMVAULT_KEY=imv_lxy8k3m9q0ab_2f8c1d9e4a7b6c0d3e5f8192a4b6c8d0

# Who am I?
curl -H "Authorization: Bearer $IMVAULT_KEY" https://img.example.com/api/v1/me

# Upload
curl -H "Authorization: Bearer $IMVAULT_KEY" \
     -F "files=@photo.jpg" \
     -F "public=1" \
     https://img.example.com/api/v1/upload
```

`X-API-Key: <key>` works anywhere `Authorization: Bearer` does.

**Upload options** (multipart form fields)

| Field | Meaning |
| --- | --- |
| `files` | One or more files. `file`, `image`, and `uploads` are accepted as aliases |
| `visibility` | `public`, `members`, or `private`. Defaults to the instance default |
| `metadata` | `shown`, `inherit`, or `hidden`. Defaults to `inherit`, which follows visibility |

The file response carries a `details` object with whatever the file said about
itself — `taken`, `camera`, `lens`, `exposure`, `aperture`, `iso`, `focal`,
`software`, and the identifying `artist`, `copyright`, `serial`, `latitude`,
`longitude`, and `altitude`. It is omitted when there is nothing to report. The
API is not masked: it returns an account's own files to that account.
| `public` | The older boolean. `1` means `visibility=public`, `0` means `private` |
| `album` | Album slug or id to add the uploads to |
| `tags` | Comma-separated tag names to apply to the uploads |

**Visibility has three levels**, not two: `public` is anyone with the link,
`members` is any signed-in account, and `private` is the uploader. An upload
that names no level gets the instance default, which is what the upload form
also preselects. Anonymous uploads are always public — there is no account to
scope a closed level to, and the link handed back to the uploader would not open
for them otherwise.

The `public` field is kept as a synonym so existing clients, including the
ShareX configuration below, keep working. Prefer `visibility`.

An anonymous upload is the only chance to tag it, so the uploader form and this
API both take tags at upload time. A signed-in account can tag its own uploads
later; so can an administrator, including anonymous ones.

**Response formats**

- Default: JSON with `files[]` (id, name, mime, kind, dimensions, tags, `page_url`,
  `raw_url`, `thumb_url`, `url`) and any per-file `errors[]`.
- `?format=text`, or `Accept: text/plain`: one bare URL per line, which is what
  ShareX-style custom uploaders expect.

A ready-to-paste ShareX custom uploader configuration:

| Setting | Value |
| --- | --- |
| Request URL | `https://img.example.com/api/v1/upload?format=text` |
| Request method | `POST` |
| Body | `Form data (multipart/form-data)` |
| File form name | `files` |
| Headers | `Authorization: Bearer <key>` |

## Albums and tags

Anonymous uploads' tags live in a shared namespace rather than an account's, and
are addressed as `/tags/~/<slug>` in the web UI. Tag JSON reports an empty
`username` for them.

Write endpoints accept **either JSON or ordinary form encoding**, so scripts and
`curl` can both be comfortable. Albums can be addressed by **slug or numeric id**
interchangeably.

An album also has an `access` level, which is who may add their own files to it:

| `access` | Who may add |
| --- | --- |
| `owner` | The creator, and administrators. The default |
| `members` | Any signed-in account that can see the album |

Contributing only ever means adding **your own** files. An album decides who may
add, never whose files may be added, so sharing an album is not a way to shuffle
somebody else's uploads around. A contributor may also take back their own file;
removing anybody else's stays with the album's owner. A shared album has to be
visible to members, since one only its owner can see is not shared with anybody.

```bash
# Create an album
curl -H "Authorization: Bearer $IMVAULT_KEY" -H 'Content-Type: application/json' \
     -d '{"title":"Summer 2026","description":"Warm ones","visibility":"public"}' \
     https://img.example.com/api/v1/albums

# One anybody on the instance may add their own images to
curl -H "Authorization: Bearer $IMVAULT_KEY" -H 'Content-Type: application/json' \
     -d '{"title":"Raid Night","visibility":"members","access":"members"}' \
     https://img.example.com/api/v1/albums

# Add uploads to it (and seed it at creation time with a "files" array)
curl -H "Authorization: Bearer $IMVAULT_KEY" -H 'Content-Type: application/json' \
     -d '{"files":["abc123","def456"]}' \
     https://img.example.com/api/v1/albums/summer-2026/files

# PATCH only touches the fields you send
curl -X PATCH -H "Authorization: Bearer $IMVAULT_KEY" \
     -H 'Content-Type: application/json' -d '{"visibility":"private"}' \
     https://img.example.com/api/v1/albums/summer-2026

# Tag a file; the response is the file's resulting tag list
curl -H "Authorization: Bearer $IMVAULT_KEY" -H 'Content-Type: application/json' \
     -d '{"name":"beach"}' \
     https://img.example.com/api/v1/files/abc123/tags

# Detach by id, slug or name
curl -X DELETE -H "Authorization: Bearer $IMVAULT_KEY" \
     https://img.example.com/api/v1/files/abc123/tags/beach

# Find things
curl -H "Authorization: Bearer $IMVAULT_KEY" \
     'https://img.example.com/api/v1/files?tag=beach&kind=image&public=1'
```

`GET /api/v1/files` accepts `q`, `tag`, `album`, `visibility`, `public`, `kind`,
`limit`, and `offset`. `kind` is one of `image`, `animated` or `video`.
`visibility` takes an exact level; `public=0` means anything that is not public,
which is what callers written against two levels meant by it.

**Endpoints**

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/me` | Identify the calling key |
| `POST` | `/api/v1/upload` | Upload one or more files |
| `GET` | `/api/v1/files` | List and filter your uploads |
| `GET` | `/api/v1/files/{id}` | Metadata for one file |
| `PATCH` | `/api/v1/files/{id}` | Change `visibility`, `metadata`, or the older `public` |
| `DELETE` | `/api/v1/files/{id}` | Delete a file and its bytes |
| `POST` | `/api/v1/files/{id}/tags` | Attach a tag |
| `DELETE` | `/api/v1/files/{id}/tags/{ref}` | Detach a tag by id, slug or name |
| `GET` | `/api/v1/tags` | List tags |
| `GET` | `/api/v1/albums` | List your albums |
| `POST` | `/api/v1/albums` | Create an album (optionally seeding `files`) |
| `GET` | `/api/v1/albums/{ref}` | Album with its files |
| `PATCH` | `/api/v1/albums/{ref}` | Update title, description, visibility or `access` |
| `DELETE` | `/api/v1/albums/{ref}` | Delete an album, keeping its files |
| `POST` | `/api/v1/albums/{ref}/files` | Add files to an album |
| `DELETE` | `/api/v1/albums/{ref}/files/{id}` | Remove a file from an album |

Keys act as the account that owns them, so a key can only ever see its owner's
uploads and albums. Referring to another account's resource returns `404`, not
`403`, so the API cannot be used to probe for what exists.

---
