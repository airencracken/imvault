# imvault

A small, self-hosted image and clip host in the spirit of
[Picsur](https://github.com/CaramelFur/Picsur),
[Chibisafe](https://github.com/chibisafe/chibisafe) and Imgur — a Go server with a
server-rendered, htmx-driven front end. No build step, no JavaScript framework, no
external services: one binary and a data directory.

```
make run          # then open http://localhost:8080
```

The first account you register becomes the administrator.

---

## Features

**Uploading**
- Drag-and-drop, file picker, or paste straight from the clipboard
- Multiple files per request (up to 20 by default)
- Still images: JPEG, PNG, WebP, BMP, TIFF — validated by actually decoding them
- Animated GIFs and animated WebPs keep their animation
- Short clips: WebM, MP4 and MOV, stored untouched and streamed with range support
- EXIF orientation is applied, so phone photos are not sideways
- Transparent images keep their alpha; opaque ones get efficient JPEG renditions

**Library**
- Per-user gallery with search by filename or id
- Albums: create, describe, mark public, add and remove images
- Tags: free-form labels, **owned per account**, added and removed inline with
  htmx; a tag index and owner-qualified tag pages
- Pagination everywhere

**Sharing**
- Every asset has a canonical page (`/f/{id}`), a short link (`/p/{id}`) and a raw URL
- Per-file public/private toggle; private files 404 for anyone but the owner
- Anonymous uploads: no account needed, subject to a retention window

**API**
- Bearer-token API keys, managed in the web UI, scoped to one account
- `POST /api/v1/upload` accepts the same files as the browser uploader
- Full CRUD for files, albums and tags, so the whole library is scriptable
- Write endpoints take JSON or form encoding; albums resolve by slug or id
- JSON by default; `?format=text` returns bare URLs for ShareX-style clients
- Keys are shown exactly once and stored only as a SHA-256 digest

**Renditions**
- A thumbnail (~480px) for grids and a preview (~1600px) for detail pages
- Animations and clips have no downscaled preview: the original *is* what the
  browser should play, so the preview endpoint serves it directly
- Clips get a poster frame extracted with ffmpeg; without ffmpeg they get a
  generated placeholder poster instead of failing

**Accounts**
- Multi-user signup; the first account becomes the administrator
- Password reset by email when SMTP is configured, or by an
  administrator-issued one-time link when it is not
- Outbound mail is queued and retried, so a relay outage delays a message
  instead of losing it
- Change your password or email address while signed in
- Optional email confirmation, which never gates access

**Housekeeping**
- Sessions and API keys are stored hashed, so a database leak does not hand over
  live credentials
- Per-account storage quotas, reserved atomically so concurrent uploads cannot
  oversubscribe
- Upload rate limiting, keyed by account when signed in and by client address for
  anonymous uploads
- A background reaper deletes expired anonymous uploads, orphaned tags, stale
  sessions and expired keys
- Double-submit-cookie CSRF protection on every cookie-authenticated mutation

**Administration**
- An admin area with instance totals, per-account usage and the heaviest accounts
- Set per-account quotas, promote or demote administrators, disable accounts
  (which revokes their sessions and keys) and delete accounts outright
- Moderate any file across the instance, crediting the owner's storage back

---

## Quick start

### Try it in one command

```bash
make demo
```

That builds the server, boots it on <http://localhost:8080> against a throwaway
`./demo-data` directory, creates a `demo` account, and uploads a few generated
samples — a large JPEG, a transparent PNG, an animated GIF, and a WebM clip when
ffmpeg is available. It prints the sign-in details and an API key, then keeps
running in the foreground until you press Ctrl-C. `make clean-demo` deletes the
data directory.

```bash
PORT=9000 make demo      # if 8080 is taken
make help                # everything else
```

### From source

```bash
make run                             # :8080, using ./data
make run PORT=9000 DATA_DIR=/tmp/imv # or somewhere else
```

Equivalently, without make:

```bash
go build -o bin/imvault ./cmd/imvault
IMVAULT_DATA_DIR=./data ./bin/imvault
```

### Docker

```bash
docker build -t imvault .
docker run -d --name imvault \
  -p 8080:8080 \
  -v imvault-data:/data \
  -e IMVAULT_ALLOW_SIGNUP=false \
  imvault
```

The image bundles ffmpeg. To slim it down, remove `ffmpeg` from the `apk add`
line in the `Dockerfile`; clips will still be accepted, just with placeholder
posters and no duration check.

### docker-compose

A `docker-compose.yml` is included, with every setting overridable from the
environment or a `.env` file:

```bash
make compose-up      # or: docker compose up --build -d
make compose-down
```

Put it behind a TLS-terminating reverse proxy, set `IMVAULT_BASE_URL` to the URL
you actually browse, and set `IMVAULT_SECURE_COOKIES=true`.

---

## Configuration

Everything is environment-driven, and every setting has a working default.

| Variable | Default | Purpose |
| --- | --- | --- |
| `IMVAULT_ADDR` | `:8080` | Listen address |
| `IMVAULT_DATA_DIR` | `./data` | Root for the database and stored objects |
| `IMVAULT_DB` | `<data>/imvault.db` | SQLite database path |
| `IMVAULT_BASE_URL` | *(derived from request)* | Absolute prefix used when building share links |
| `IMVAULT_ALLOW_SIGNUP` | `true` | Whether new accounts can register |
| `IMVAULT_ALLOW_ANONYMOUS_UPLOADS` | `true` | Whether logged-out visitors may upload |
| `IMVAULT_ANONYMOUS_TTL` | `24h` | How long anonymous uploads survive |
| `IMVAULT_SESSION_TTL` | `720h` | Login session lifetime |
| `IMVAULT_CLEANUP_INTERVAL` | `15m` | How often the reaper runs |
| `IMVAULT_MAX_UPLOAD_BYTES` | `33554432` (32 MiB) | Per-file limit for images and animations |
| `IMVAULT_MAX_VIDEO_BYTES` | `134217728` (128 MiB) | Per-file limit for clips |
| `IMVAULT_MAX_VIDEO_DURATION` | `60s` | Longest accepted clip |
| `IMVAULT_DEFAULT_QUOTA_BYTES` | `5368709120` (5 GiB) | Storage cap given to new accounts; `0` for unlimited |
| `IMVAULT_SMTP_HOST` | *(empty)* | Mail relay. Empty disables email entirely |
| `IMVAULT_SMTP_PORT` | `587` | Relay port |
| `IMVAULT_SMTP_USERNAME` | *(empty)* | Skip authentication when empty, for a local relay |
| `IMVAULT_SMTP_PASSWORD` | *(empty)* | Relay password |
| `IMVAULT_SMTP_FROM` | `imvault <no-reply@localhost>` | Envelope and header sender |
| `IMVAULT_SMTP_TLS` | `starttls` | `starttls`, `implicit` (port 465) or `none` |
| `IMVAULT_PASSWORD_RESET_TTL` | `1h` | How long a reset link stays valid |
| `IMVAULT_EMAIL_VERIFY_TTL` | `24h` | How long a confirmation link stays valid |
| `IMVAULT_MAIL_MAX_ATTEMPTS` | `5` | Delivery attempts before a message is parked as failed |
| `IMVAULT_MAIL_RETRY_INTERVAL` | `1m` | How often the queue is swept |
| `IMVAULT_UPLOAD_RATE_PER_HOUR` | `120` | Uploads per hour per identity; `0` disables limiting |
| `IMVAULT_UPLOAD_BURST` | `20` | How many uploads may be made back to back |
| `IMVAULT_TRUST_PROXY_HEADERS` | `false` | Read the client address from `X-Forwarded-For` / `X-Real-IP` |
| `IMVAULT_FFMPEG` | `ffmpeg` | ffmpeg binary; a missing binary only disables clip posters |
| `IMVAULT_FFPROBE` | `ffprobe` | ffprobe binary |
| `IMVAULT_JPEG_QUALITY` | `82` | Encoder quality for lossy renditions |
| `IMVAULT_THUMB_MAX` | `480` | Thumbnail bounding box in pixels |
| `IMVAULT_PREVIEW_MAX` | `1600` | Preview bounding box in pixels |
| `IMVAULT_SECURE_COOKIES` | `false` | Set the `Secure` flag on cookies |
| `IMVAULT_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

Durations accept Go syntax (`24h`, `90m`) or a bare number of seconds.

### Quotas

Every account has a storage cap, defaulting to `IMVAULT_DEFAULT_QUOTA_BYTES` at
creation and settable per account from `/admin/users`. **Zero means unlimited**,
which is what accounts created before quotas existed are left at — retrofitting
a cap onto existing users would silently break them.

The quota counts **original upload bytes**, not the derived thumbnails and
previews, so the number a user sees corresponds to the files they uploaded.

Enforcement is two-stage. A cheap pre-flight rejects an upload that obviously
cannot fit before anything is decoded or written; then, once the real byte count
is known, the bytes are claimed with a single conditional `UPDATE`:

```sql
UPDATE users SET storage_used = storage_used + :size
WHERE id = :id AND (quota_bytes = 0 OR storage_used + :size <= quota_bytes)
```

Because the check and the claim are one statement, two concurrent uploads cannot
both fit into room that exists only once. Every failure path — a failed
rendition, a failed insert, a rejected request — releases what was claimed, and
the total is clamped at zero so a double release cannot drive it negative.
Deleting a file credits the owner back.

### Administration

The first account registered is the administrator; the role can be granted and
revoked from `/admin/users`. Two operations are refused to avoid locking
everybody out: demoting the last administrator, and deleting the last
administrator.

### Password reset

Two paths, chosen by whether `IMVAULT_SMTP_HOST` is set.

With a relay configured, `/forgot` takes a username or address, mints a
single-use token and emails the link. The response is identical whether or not
the account exists, so the endpoint cannot be used to enumerate accounts.

Without a relay, `/forgot` says so plainly and an administrator issues the link
from `/admin/users`. That link is rendered straight into the response rather than
redirected to, so it never lands in a URL, a browser history entry or a server
log, and it is not stored anywhere.

Either way the token is stored as a SHA-256 digest, works exactly once, is
scoped to its purpose — a confirmation token cannot open the reset form — and
issuing a new one invalidates the previous.

Mail is written to an `outbound_mail` table *before* delivery is attempted, so a
relay that is down, restarting or misconfigured delays a message rather than
losing it. A worker retries with a growing backoff (roughly 1m, 4m, 16m, 1h,
4h), and after `IMVAULT_MAIL_MAX_ATTEMPTS` the message is parked as failed and
surfaced at `/admin/mail`, where it can be requeued or discarded by hand. This
also means a message survives a restart: the queue is in the database, not in
memory. Changing a password signs out every
other device; API keys are deliberately left alone, since revoking a script's
credentials on an ordinary password change would be surprising.

Email confirmation is available but never gates anything: an unverified address
works exactly like a verified one. The flag exists so an operator can see who
has proved their address.

Disabling an account revokes its live sessions and every API key it holds, so it
loses access immediately rather than at the next login. Deleting an account
collects its storage keys *before* the database rows cascade away, so the bytes
are removed too rather than being orphaned on disk.

### Rate limiting

Uploads are limited with an in-memory token bucket: each identity gets
`IMVAULT_UPLOAD_BURST` uploads up front, refilling at
`IMVAULT_UPLOAD_RATE_PER_HOUR`, and anything over budget gets a `429` with a
`Retry-After` header. The bucket is keyed by account when signed in and by client
address for anonymous uploads, so the open upload path is covered too. Reads are
never limited.

The limiter is process-local and resets on restart, which is the right trade for
a single self-hosted instance: no extra dependency, no shared state.

**Behind a reverse proxy**, every request arrives from the proxy's address, so
anonymous uploads would all share one bucket. Set
`IMVAULT_TRUST_PROXY_HEADERS=true` to read the client from `X-Forwarded-For`
instead — but only do so behind a proxy you control, since those headers are
otherwise client-supplied and would let a caller sidestep the limit entirely.

---

## Media handling

Every upload is classified from its leading bytes, not its filename:

| Kind | Detected as | Stored | Grid tile | Detail view |
| --- | --- | --- | --- | --- |
| `image` | anything the image decoder accepts | original + thumb + preview | thumbnail | preview image |
| `animated` | GIF with more than one frame, or WebP with the animation flag | original + thumb | still first frame, swaps to the animation on hover | the animation itself |
| `video` | WebM/Matroska, MP4, MOV | original + poster | poster with a play badge | `<video>` player |

Animated assets are never re-encoded: the original bytes are served, so quality
and timing are preserved exactly. Clips are likewise stored untouched — imvault
is a host, not a transcoder.

ffmpeg is used in two narrow ways for clips: `ffprobe` reads dimensions and
duration (so the duration limit can be enforced), and `ffmpeg` extracts one
still frame for the poster. If neither binary is present, imvault logs a warning
at startup and keeps working — clips are accepted on their magic bytes and get a
generated placeholder poster.

---

## Routes

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/` | Landing page, or redirect to your gallery |
| `GET` `POST` | `/login`, `/register` | Authentication |
| `POST` | `/logout` | End the session |
| `GET` `POST` | `/forgot` | Request a password reset |
| `GET` `POST` | `/reset/{token}` | Choose a new password |
| `GET` | `/verify/{token}` | Confirm an email address |
| `GET` `POST` | `/settings/password` | Change password, manage email |
| `POST` | `/settings/email` | Set or clear the email address |
| `GET` | `/gallery` | Your uploads (supports `?q=` and `?page=`) |
| `GET` `POST` | `/upload` | Uploader and upload endpoint |
| `GET` | `/f/{id}` | Asset detail page |
| `GET` | `/f/{id}/raw` | Original file (range requests supported) |
| `GET` | `/f/{id}/thumb` | Thumbnail or clip poster |
| `GET` | `/f/{id}/preview` | Preview, or the original for animations and clips |
| `POST` | `/f/{id}/visibility` | Toggle public/private |
| `POST` | `/f/{id}/delete` | Delete the file |
| `POST` | `/f/{id}/tags`, `/f/{id}/tags/{tagID}/delete` | Attach or detach a tag |
| `GET` `POST` | `/albums` | List and create albums |
| `GET` | `/a/{slug}` | Album page |
| `POST` | `/a/{slug}/delete` | Delete an album (keeps its files) |
| `POST` | `/a/{slug}/files`, `/a/{slug}/files/{fileID}/delete` | Add or remove album members |
| `GET` | `/tags` | Tag index |
| `GET` | `/tags/{username}/{slug}` | Files carrying one account's tag |
| `GET` | `/p/{id}` | Short share link (redirects to `/f/{id}`) |
| `GET` `POST` | `/settings/api-keys` | Manage API keys |
| `POST` | `/settings/api-keys/{id}/delete` | Revoke a key |
| `GET` | `/admin` | Admin overview |
| `GET` | `/admin/users` | Account list with usage |
| `POST` | `/admin/users/{id}/quota` | Set an account's storage cap |
| `POST` | `/admin/users/{id}/admin` | Grant or revoke administrator rights |
| `POST` | `/admin/users/{id}/disabled` | Disable or enable an account |
| `POST` | `/admin/users/{id}/delete` | Delete an account and its files |
| `POST` | `/admin/users/{id}/reset` | Issue a one-time password reset link |
| `GET` | `/admin/mail` | Outbound mail queue |
| `POST` | `/admin/mail/{id}/retry` | Requeue a failed message |
| `POST` | `/admin/mail/{id}/delete` | Discard a message |
| `POST` | `/admin/maintenance/storage` | Recalculate per-account storage usage |
| `GET` | `/admin/files` | Every upload, for moderation |
| `POST` | `/admin/files/{id}/delete` | Delete any file |
| `GET` | `/healthz` | Liveness probe |

Mutating requests that use cookies must carry the CSRF token, either as the
`X-CSRF-Token` header (htmx does this automatically) or as a `csrf_token` form
field. The `/api/v1/...` endpoints never use cookies and are exempt.

---

## API

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
| `files` | One or more files. `file`, `image` and `uploads` are accepted as aliases |
| `public` | `1` to make the uploads link-shareable. **API uploads default to private** |
| `album` | Album slug or id to add the uploads to |

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

### Albums and tags

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

`GET /api/v1/files` accepts `q`, `tag`, `album`, `public`, `kind`, `limit` and
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

## How it fits together

```
cmd/imvault          entrypoint: config, wiring, graceful shutdown
internal/config      environment-driven configuration
internal/db          SQLite connection + embedded migrations
internal/models      shared data types
internal/store       all SQL; one file per aggregate
internal/storage     object storage behind a Backend interface (disk today)
internal/imaging     decode, orient, downscale, encode still images
internal/media       classify uploads; animation and video handling
internal/ratelimit   in-memory token bucket for upload limiting
internal/mail        SMTP delivery, optional and degradable
internal/tokens      opaque secret generation and hashing
internal/apikeys     API key generation, splitting and verification
internal/web         routing, middleware, handlers, templates, static assets
scripts/demo.sh      boots a seeded throwaway instance (`make demo`)
scripts/genmedia     generates the demo's sample images
```

A few decisions worth knowing about:

- **One visibility predicate.** Every listing, every count and the tag index go
  through a single `visibilityClause`, so what a listing shows can never diverge
  from what the routes serving the bytes will allow. Retention is folded into the
  same predicate: a lapsed upload disappears the moment it expires, not whenever
  the reaper next runs.
- **Tags belong to accounts.** Two people can both use "beach" without sharing a
  label, a count or a lifetime, and a tag is addressed as
  `/tags/{username}/{slug}` because the slug alone is only unique within one
  account. Visibility is still applied when the vocabulary is *exposed*: the tag
  index and its counts are computed over the files you can see, so a tag that
  only labels somebody else's private upload never appears. Tags live with the
  **file's** owner rather than whoever did the tagging, so an administrator
  editing someone else's upload does not leave their own labels on it.
- **SQLite via `modernc.org/sqlite`**, a pure-Go driver, so the binary needs no cgo
  and cross-compiles trivially. The pool is pinned to a single connection, which
  sidesteps `SQLITE_BUSY` entirely and is far beyond this workload's needs.
- **Timestamps are `INTEGER` unix seconds**, so comparisons never depend on driver
  time handling.
- **GIF frame counting walks the block structure** rather than calling
  `gif.DecodeAll`, which would decode every frame into memory just to answer
  "is this animated?".
- **Two template sets.** Pages are parsed as `layout + partials + one page`, and a
  separate set holds the partials alone for htmx swaps. The same `file_grid`
  fragment renders inside a full page and as an htmx response, so markup lives in
  one place.
- **ffmpeg is optional by design.** Anything that depends on it degrades to a
  placeholder and a logged warning rather than a failed upload.
- **Row first, then bytes.** Deletes remove the database row before the objects, so
  a crash leaves harmless orphaned files rather than dangling references.
- **Storage is an interface.** Swapping the disk backend for S3 means implementing
  `storage.Backend`; no handler changes.

### Data layout

```
data/
├── imvault.db
└── objects/
    ├── orig/YYYY/MM/<id>.<ext>       # untouched upload
    ├── thumb/YYYY/MM/<id>.<ext>      # grid thumbnail / clip poster
    └── preview/YYYY/MM/<id>.<ext>    # detail rendition (still images only)
```

File ids are random 12-character base36 strings — unguessable, and private files
are still access-checked rather than relying on that.

---

## Development

```bash
make help         # list every target
make check        # gofmt + go vet + go test, which is what CI should run
make test-race
make build
make demo         # a seeded instance to click around in
```

The test suite covers the image pipeline (including the header-rewind path and
transparency handling), media classification and GIF frame counting, API key
generation and verification, the store (visibility rules, LIKE escaping,
cascades, tag pruning, album slug collisions, key expiry) and the HTTP layer end
to end — registration, multipart upload, renditions, animations, clips with
range requests, tagging, albums, the JSON and plain-text API, and CSRF
enforcement, all against the real templates.

Tests that need ffmpeg skip themselves when it is unavailable, so the suite runs
anywhere.

---

## Limitations

Worth being upfront about what this deliberately does **not** do yet:

- No transcoding. Clips are stored exactly as uploaded, so whatever the browser
  supports is what you get. An MP4 with an exotic codec will not play.
- Animated WebP thumbnails depend on `golang.org/x/image/webp` decoding the first
  frame; if it cannot, the tile falls back to a placeholder poster.
- No video streaming beyond HTTP range requests — no HLS or adaptive bitrate.
- No 2FA, and no self-service account deletion.
- Tags cannot be applied to anonymous uploads: those files have no owning account
  for a tag to live in.
- API keys can manage files, albums and tags, but not account settings
  (password, email) or key management. No admin operations are exposed over the
  API either — the admin area is web-only.
- Password reset by email needs a working relay; an instance without one falls
  back to administrator-issued links.
- The mail queue is swept on a timer, so a message queued just after a sweep
  waits up to `IMVAULT_MAIL_RETRY_INTERVAL` before its first retry.
- De-duplication: `sha256` is recorded for every upload but identical files are
  stored again rather than shared.
- Rate limiting is in-memory and per process, so running several instances behind
  a load balancer gives each its own budget.
- Storage usage can drift if the process is killed mid-upload; the admin
  dashboard has a button to recalculate it from the files table.

## License

Copyright (C) 2026 Marcus J. Hildum

This program is free software: you can redistribute it and/or modify it under
the terms of the **GNU Affero General Public License** as published by the Free
Software Foundation, either version 3 of the License, or (at your option) any
later version.

It is distributed in the hope that it will be useful, but **without any
warranty**; without even the implied warranty of merchantability or fitness for
a particular purpose. See the [GNU AGPL](LICENSE) for details.

Because the AGPL covers software used over a network, running a modified version
as a service obliges you to offer its users the corresponding source. The footer
carries a source link for exactly that reason; point `IMVAULT_SOURCE_URL` at your
own fork if you have modified it, or set it to empty to remove the link.

Every source file carries an `SPDX-License-Identifier: AGPL-3.0-or-later` tag.
