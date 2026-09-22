# Configuration

Object storage defaults to local disk. See [Storage](storage.md) for optional
S3-compatible storage, credentials, endpoint settings, and migration.

Every setting is an environment variable, and every one has a working default.
It can be run with no configuration at all.

Three of them are also editable while it is running; see [Instance
settings](#instance-settings) below.

| Variable | Default | Purpose |
| --- | --- | --- |
| `IMVAULT_ADDR` | `:8080` | Listen address |
| `IMVAULT_DATA_DIR` | `./data` | Root for the database and stored objects |
| `IMVAULT_DB` | `<data>/imvault.db` | SQLite database path |
| `IMVAULT_BASE_URL` | *(derived from request)* | Absolute prefix used when building share links |
| `IMVAULT_ALLOW_SIGNUP` | `true` | Whether new accounts can register |
| `IMVAULT_INVITE_ONLY` | `false` | Whether registering needs an invitation code |
| `IMVAULT_ALLOW_ANONYMOUS_UPLOADS` | `true` | Whether logged-out visitors may upload |
| `IMVAULT_ANONYMOUS_TTL` | `24h` | How long anonymous uploads survive |
| `IMVAULT_DEFAULT_VISIBILITY` | `members` | What a new upload is visible to: `public`, `members`, or `private` |
| `IMVAULT_SESSION_TTL` | `720h` | Login session lifetime |
| `IMVAULT_CLEANUP_INTERVAL` | `15m` | How often the reaper runs |
| `IMVAULT_MAX_UPLOAD_BYTES` | `33554432` (32 MiB) | Per-file limit for images and animations |
| `IMVAULT_MAX_VIDEO_BYTES` | `134217728` (128 MiB) | Per-file limit for clips |
| `IMVAULT_MAX_VIDEO_DURATION` | `60s` | Longest accepted clip |
| `IMVAULT_DEFAULT_QUOTA_BYTES` | `5368709120` (5 GiB) | Storage cap given to new accounts; `0` for unlimited |
| `IMVAULT_MAX_TOTAL_BYTES` | `0` | Ceiling for the whole instance; `0` for none |
| `IMVAULT_MAX_CONCURRENT_UPLOADS` | one per CPU, minimum 2 | Uploads processed at once, across everybody |
| `IMVAULT_SMTP_HOST` | *(empty)* | Mail relay. Empty disables email entirely |
| `IMVAULT_SMTP_PORT` | `587` | Relay port |
| `IMVAULT_SMTP_USERNAME` | *(empty)* | Skip authentication when empty, for a local relay |
| `IMVAULT_SMTP_PASSWORD` | *(empty)* | Relay password |
| `IMVAULT_SMTP_FROM` | `imvault <no-reply@localhost>` | Envelope and header sender |
| `IMVAULT_SMTP_TLS` | `starttls` | `starttls`, `implicit` (port 465) or `none` |
| `IMVAULT_TOTP_ISSUER` | `imvault` | Name an authenticator app shows for the account |
| `IMVAULT_SECRET_KEY` | *(empty)* | Key for encrypting TOTP secrets; overrides the key file |
| `IMVAULT_SECRET_KEY_FILE` | `<data>/secret.key` | Where that key is kept, created on first run |
| `IMVAULT_LOGIN_RATE_PER_HOUR` | `30` | Sign-in attempts per hour per address, shared by password and second-factor steps; `0` disables |
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
| `IMVAULT_OIDC_ISSUER` | *(empty)* | OpenID Connect issuer. Empty disables provider sign-in |
| `IMVAULT_OIDC_CLIENT_ID` | *(empty)* | Client id registered with the provider |
| `IMVAULT_OIDC_CLIENT_SECRET` | *(empty)* | Client secret, for a confidential client |
| `IMVAULT_OIDC_NAME` | `Single sign-on` | What the button says |
| `IMVAULT_OIDC_SCOPES` | `openid profile email` | Comma or space separated scopes |
| `IMVAULT_OIDC_ALLOWED_DOMAINS` | *(empty)* | Email domains allowed to sign in |

Durations accept Go syntax (`24h`, `90m`) or a bare number of seconds.

## Signing in with a provider

Setting `IMVAULT_OIDC_ISSUER` and `IMVAULT_OIDC_CLIENT_ID` turns on sign-in
through any OpenID Connect provider — Keycloak, Authentik, Google, Discord, or
anything else that speaks it. Discovery is automatic from
`<issuer>/.well-known/openid-configuration`.

```bash
IMVAULT_BASE_URL=https://img.example.com
IMVAULT_OIDC_ISSUER=https://auth.example.com/application/o/imvault/
IMVAULT_OIDC_CLIENT_ID=imvault
IMVAULT_OIDC_CLIENT_SECRET=…
IMVAULT_OIDC_NAME="Example sign-in"
```

Register the redirect URI **`<IMVAULT_BASE_URL>/auth/oidc/callback`** with the
provider, exactly. `IMVAULT_BASE_URL` is required when a provider is
configured, because the redirect URI has to match what was registered and
deriving it from a request's `Host` header would let the caller choose it.

Sign-in is supplementary: passwords keep working, and every account keeps one,
so a provider being down is an inconvenience rather than a lockout.

An issuer that cannot be reached at startup does **not** stop the instance from
running. The provider is resolved on first use, so a fix takes effect without a
restart.

## Instance settings

Three instance-wide policies are editable at `/admin/settings`, because all of
them are things an operator may need to change in a hurry:

| Setting | Variable it falls back to |
| --- | --- |
| Whether new accounts can register | `IMVAULT_ALLOW_SIGNUP` |
| Whether registering needs an invitation | `IMVAULT_INVITE_ONLY` |
| Whether logged-out visitors may upload | `IMVAULT_ALLOW_ANONYMOUS_UPLOADS` |
| How long anonymous uploads survive | `IMVAULT_ANONYMOUS_TTL` |
| What a new upload is visible to | `IMVAULT_DEFAULT_VISIBILITY` |
| How much the instance will store in total | `IMVAULT_MAX_TOTAL_BYTES` |

The same page offers the three ways of running an instance as one-click
profiles, because personal, group, and public hosts are the same program at
different settings rather than three programs:

| Profile | Joining | Anonymous uploads | New uploads are visible to |
| --- | --- | --- | --- |
| **Personal** | closed | off | private |
| **Group** | by invitation | off | members |
| **Public** | open | allowed, expiring | public |

Applying a profile deliberately leaves the retention window and the storage
ceiling alone. The first reaches backwards over uploads already stored, and a
button labelled "Public" should not quietly purge somebody's files; the second
is a fact about the machine rather than a shape of instance, and a profile has
no business guessing how much disk the operator has.

Read and closed are not the same thing. An invitation **always** admits, even
when registration is switched off entirely, because it is a deliberate grant by
an administrator rather than a public door. That is what makes "close the
instance, then invite the people you want" work; without it the only way to add
somebody to a closed instance was to edit the database by hand. Codes are issued
at `/admin/invites`.

**The variable seeds the setting; the interface takes over once you save.** A row
is written only when an administrator actually changes something, so a
configuration variable still applies to anything nobody has touched, and a fresh
instance behaves exactly as its configuration says. The page states, for each
setting, whether it is currently coming from the file or from the interface, and
**Clear stored settings** removes the overrides to hand control back to the
environment.

The retention window accepts a bare number of hours, or a duration with a unit:
`24` and `24h` mean the same thing, and `7d` is a week.

Changing the retention window **rewrites the deadline on anonymous uploads that
already exist**, so shortening it takes effect immediately instead of whenever
the old deadlines happen to pass. Deadlines stay measured from when each upload
arrived, so anything older than a newly shortened window becomes collectable on
the reaper's next pass. Without this, an abuse response would not bite until the
old deadlines expired.

The retention window must be positive. To stop anonymous uploads altogether,
turn them off rather than setting the window to zero.

## Storage ceilings

There are two separate ceilings, and they answer different questions.

The **instance ceiling** (`IMVAULT_MAX_TOTAL_BYTES`, or the same field under
Instance settings) is how much the machine will hold in total: every account's
uploads and every anonymous upload added together. Once it is reached, uploads
are refused with a message saying so. It is measured against the stored
originals, which is the same number the dashboard shows as "stored", and it is
derived from the files table rather than kept as a running counter, so it cannot
drift out of step with what is actually there.

The **per-account quota** is the other ceiling, and it is set one account at a
time at `/admin/users`:

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

## Concurrency

`IMVAULT_MAX_CONCURRENT_UPLOADS` bounds how many uploads are being processed at
once, across everybody. It defaults to one per CPU, with a floor of two.

This is a different control from rate limiting and an instance needs both. A
token bucket bounds how many uploads one identity may start per hour, but its
burst allowance still lets that identity start several at the same instant, and
every identity gets its own bucket. What neither bounds is the total, and a
video upload runs ffmpeg inside the request, so the sum of simultaneous
invocations was (identities × burst) with no ceiling at all. On a small machine
that is a one-person outage.

A request that cannot get a slot within ten seconds is refused with a `503` and
a `Retry-After`, rather than being queued indefinitely. A browser uploading
photos sees the same message in place instead of a failed page.

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
