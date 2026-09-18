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
| `public` | `1` to make the uploads link-shareable. **API uploads default to private** |
| `album` | Album slug or id to add the uploads to |
| `tags` | Comma-separated tag names to apply to the uploads |

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

```bash
# Create an album
curl -H "Authorization: Bearer $IMVAULT_KEY" -H 'Content-Type: application/json' \
     -d '{"title":"Summer 2026","description":"Warm ones","public":true}' \
     https://img.example.com/api/v1/albums

# Add uploads to it (and seed it at creation time with a "files" array)
curl -H "Authorization: Bearer $IMVAULT_KEY" -H 'Content-Type: application/json' \
     -d '{"files":["abc123","def456"]}' \
     https://img.example.com/api/v1/albums/summer-2026/files

# PATCH only touches the fields you send
curl -X PATCH -H "Authorization: Bearer $IMVAULT_KEY" \
     -H 'Content-Type: application/json' -d '{"public":false}' \
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

`GET /api/v1/files` accepts `q`, `tag`, `album`, `public`, `kind`, `limit`, and
`offset`. `kind` is one of `image`, `animated` or `video`.

**Endpoints**

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/me` | Identify the calling key |
| `POST` | `/api/v1/upload` | Upload one or more files |
| `GET` | `/api/v1/files` | List and filter your uploads |
| `GET` | `/api/v1/files/{id}` | Metadata for one file |
| `PATCH` | `/api/v1/files/{id}` | Change visibility (`public`) |
| `DELETE` | `/api/v1/files/{id}` | Delete a file and its bytes |
| `POST` | `/api/v1/files/{id}/tags` | Attach a tag |
| `DELETE` | `/api/v1/files/{id}/tags/{ref}` | Detach a tag by id, slug or name |
| `GET` | `/api/v1/tags` | List tags |
| `GET` | `/api/v1/albums` | List your albums |
| `POST` | `/api/v1/albums` | Create an album (optionally seeding `files`) |
| `GET` | `/api/v1/albums/{ref}` | Album with its files |
| `PATCH` | `/api/v1/albums/{ref}` | Update title, description or visibility |
| `DELETE` | `/api/v1/albums/{ref}` | Delete an album, keeping its files |
| `POST` | `/api/v1/albums/{ref}/files` | Add files to an album |
| `DELETE` | `/api/v1/albums/{ref}/files/{id}` | Remove a file from an album |

Keys act as the account that owns them, so a key can only ever see its owner's
uploads and albums. Referring to another account's resource returns `404`, not
`403`, so the API cannot be used to probe for what exists.

---
