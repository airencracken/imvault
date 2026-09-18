# Configuration

Every setting is an environment variable, and every one has a working
default. It can be run with no configuration at all.

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
| `IMVAULT_TOTP_ISSUER` | `imvault` | Name an authenticator app shows for the account |
| `IMVAULT_SECRET_KEY` | *(empty)* | Key for encrypting TOTP secrets; overrides the key file |
| `IMVAULT_SECRET_KEY_FILE` | `<data>/secret.key` | Where that key is kept, created on first run |
| `IMVAULT_LOGIN_RATE_PER_HOUR` | `30` | Sign-in attempts per hour per address and name; `0` disables |
| `IMVAULT_LOGIN_BURST` | `10` | Sign-in attempts allowed back to back |
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

## Quotas

Two ceilings exist per account, and both are set together at `/admin/users`:

| | |
| --- | --- |
| **Total storage** | Everything the account holds, added up. `0` means unlimited |
| **Largest single file** | `0` means the instance defaults apply |

The single-file ceiling *replaces* those defaults rather than merely lowering
them, so an administrator can raise it for somebody trusted as well as impose a
tighter one. The instance-wide defaults are `IMVAULT_MAX_UPLOAD_BYTES` for
images and animations, and `IMVAULT_MAX_VIDEO_BYTES` for clips.


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

## Rate limiting

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
