# Configuration

Every setting is an environment variable with a working default, so Imvault
runs with no configuration at all. Some policies can also be changed while it
runs; see [instance settings](#instance-settings).

An empty variable counts as unset unless the table says otherwise. A value
Imvault cannot read, such as `IMVAULT_ALLOW_SIGNUP=no` or
`IMVAULT_MAX_UPLOAD_BYTES=32MB`, stops the server with an error naming the
variable. Booleans accept `true`/`false`, `1`/`0` and `t`/`f`. Sizes are whole
bytes. Durations are Go durations such as `90s` or `24h`, or whole seconds.

| Variable | Default | Purpose |
| --- | --- | --- |
| `IMVAULT_ADDR` | `:8080` | Listen address |
| `IMVAULT_DATA_DIR` | `./data` | Root for the database and stored objects |
| `IMVAULT_DB` | `<data>/imvault.db` | SQLite database path |
| `IMVAULT_WITMOOT_URL` | *(empty)* | Optional Witmoot base URL for explicit album discussion drafts; requires configured IMVAULT_BASE_URL |
| `IMVAULT_BASE_URL` | *(derived from request)* | Absolute prefix used when building share links; required when mail or OpenID Connect is configured |
| `IMVAULT_NAME` | `Imvault` | Default name shown in the page title, header, and footer |
| `IMVAULT_SOURCE_URL` | `https://github.com/airencracken/imvault` | Source link shown in the footer; set it to an empty value to hide it |
| `IMVAULT_ALLOW_SIGNUP` | `true` | Whether new accounts can register |
| `IMVAULT_INVITE_ONLY` | `true` | Whether registering needs an invitation code |
| `IMVAULT_ALLOW_ANONYMOUS_UPLOADS` | `false` | Whether logged-out visitors may upload |
| `IMVAULT_ANONYMOUS_TTL` | `24h` | How long anonymous uploads survive |
| `IMVAULT_DEFAULT_VISIBILITY` | `members` | What a new upload is visible to: `public`, `members`, or `private` |
| `IMVAULT_SESSION_TTL` | `720h` | Login session lifetime; must be positive |
| `IMVAULT_CLEANUP_INTERVAL` | `15m` | How often the reaper runs; must be positive |
| `IMVAULT_MAX_UPLOAD_BYTES` | `33554432` (32 MiB) | Per-file limit for images and animations |
| `IMVAULT_MAX_VIDEO_BYTES` | `134217728` (128 MiB) | Per-file limit for clips |
| `IMVAULT_MAX_VIDEO_DURATION` | `60s` | Longest accepted clip |
| `IMVAULT_DEFAULT_QUOTA_BYTES` | `5368709120` (5 GiB) | Storage cap given to new accounts; `0` for unlimited |
| `IMVAULT_MAX_TOTAL_BYTES` | `0` | Ceiling for the whole instance; `0` for none |
| `IMVAULT_MAX_CONCURRENT_UPLOADS` | `1` | Uploads processed at once, across everybody |
| `IMVAULT_SMTP_HOST` | *(empty)* | Mail relay. Empty disables email entirely; setting it requires `IMVAULT_BASE_URL` |
| `IMVAULT_SMTP_PORT` | `587` | Relay port |
| `IMVAULT_SMTP_USERNAME` | *(empty)* | Skip authentication when empty, for a local relay |
| `IMVAULT_SMTP_PASSWORD` | *(empty)* | Relay password |
| `IMVAULT_SMTP_FROM` | `Imvault <no-reply@localhost>` | Envelope and header sender, checked at startup; one address, optionally with a display name |
| `IMVAULT_SMTP_TLS` | `starttls` | `starttls`, `implicit` (port 465) or `none`; any other value stops startup |
| `IMVAULT_TOTP_ISSUER` | `Imvault` | Name an authenticator app shows for the account |
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
| `IMVAULT_TRUSTED_PROXIES` | *(empty)* | Comma-separated proxy addresses or CIDR prefixes, such as `127.0.0.1/32,::1/128`, whose `X-Forwarded-For` and `X-Forwarded-Proto` are believed; empty believes none |
| `IMVAULT_FFMPEG` | `ffmpeg` | ffmpeg binary; a missing binary, or an empty value, only disables clip posters |
| `IMVAULT_FFPROBE` | `ffprobe` | ffprobe binary; an empty value disables clip tooling |
| `IMVAULT_JPEG_QUALITY` | `82` | Encoder quality for lossy renditions |
| `IMVAULT_THUMB_MAX` | `480` | Thumbnail bounding box in pixels |
| `IMVAULT_PREVIEW_MAX` | `1600` | Preview bounding box in pixels |
| `IMVAULT_MAP_URL` | *(empty)* | Static-map URL template with `{lat}`, `{lon}`, `{zoom}`, `{key}` placeholders; empty disables the map |
| `IMVAULT_MAP_KEY` | *(empty)* | Map provider key, substituted server-side and never sent to the browser |
| `IMVAULT_MAP_ZOOM` | `13` | Zoom level for the map and the OpenStreetMap link |
| `IMVAULT_SECURE_COOKIES` | automatic for HTTPS | HTTPS base URLs always enable `Secure`; set `true` to require it elsewhere too |
| `IMVAULT_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `IMVAULT_OIDC_ISSUER` | *(empty)* | OpenID Connect issuer. Empty disables provider sign-in |
| `IMVAULT_OIDC_CLIENT_ID` | *(empty)* | Client id registered with the provider |
| `IMVAULT_OIDC_CLIENT_SECRET` | *(empty)* | Client secret, for a confidential client |
| `IMVAULT_OIDC_NAME` | `Single sign-on` | What the button says |
| `IMVAULT_OIDC_SCOPES` | `openid profile email` | Comma or space separated scopes |
| `IMVAULT_OIDC_ALLOWED_DOMAINS` | *(empty)* | Email domains allowed to sign in; the provider must assert `email_verified` |

Settings documented elsewhere:

- Storage (local disk by default) and S3 (`IMVAULT_STORAGE`, `IMVAULT_S3_*`, `IMVAULT_DEST_*`): [Storage](storage.md)
- Backups (`IMVAULT_BACKUP_*`): [automatic backups](automatic-backups.md)
- App links and shared sign-in (`IMVAULT_APPS`, `IMVAULT_SHARED_*`): [Comfyware connections](suite.md)
- Confinement (`IMVAULT_MEDIA_SANDBOX`, `IMVAULT_BWRAP`, and `IMVAULT_SANDBOX` for OpenRC): [Linux confinement](sandbox.md)

## Signing in with a provider

Set `IMVAULT_OIDC_ISSUER` and `IMVAULT_OIDC_CLIENT_ID` to allow sign-in through
any OpenID Connect provider, such as Keycloak, Authentik, Google or Discord.
Imvault discovers the provider from `<issuer>/.well-known/openid-configuration`.
Write the issuer exactly as the provider publishes it, trailing slash and all;
a mismatch is refused.

```bash
IMVAULT_BASE_URL="https://img.example.com"
IMVAULT_OIDC_ISSUER="https://auth.example.com/application/o/imvault/"
IMVAULT_OIDC_CLIENT_ID="imvault"
IMVAULT_OIDC_CLIENT_SECRET="…"
IMVAULT_OIDC_NAME="Example sign-in"
```

Register **`<IMVAULT_BASE_URL>/auth/oidc/callback`** as the redirect URI,
exactly. `IMVAULT_BASE_URL` is required here so a request's `Host` header cannot
choose the redirect.

Passwords keep working alongside the provider, so an outage there does not lock
anyone out. An unreachable issuer does not stop startup; Imvault contacts it on
first use, so a fix takes effect without a restart.

## Instance settings

Administrators can change these policies at `/admin/settings`:

| Setting | Variable it falls back to |
| --- | --- |
| Whether new accounts can register | `IMVAULT_ALLOW_SIGNUP` |
| Whether registering needs an invitation | `IMVAULT_INVITE_ONLY` |
| Whether logged-out visitors may upload | `IMVAULT_ALLOW_ANONYMOUS_UPLOADS` |
| How long anonymous uploads survive | `IMVAULT_ANONYMOUS_TTL` |
| What a new upload is visible to | `IMVAULT_DEFAULT_VISIBILITY` |
| How much the instance will store in total | `IMVAULT_MAX_TOTAL_BYTES` |

**The variable seeds the setting; the page takes over once you save.** A stored
value exists only after an administrator changes something, so untouched
settings still follow the environment. The page shows where each value comes
from, and **Clear stored settings** hands control back to the environment.

The page also offers three one-click profiles:

| Profile | Joining | Anonymous uploads | New uploads are visible to |
| --- | --- | --- | --- |
| **Personal** | closed | off | private |
| **Group** | by invitation | off | members |
| **Public** | open | allowed, expiring | public |

A profile leaves the retention window and the storage ceiling alone. Changing
retention reaches back over files already stored, and the ceiling depends on
the machine, not the kind of instance.

An invitation **always** admits, even with registration switched off. Close the
instance, then invite the people you want. Issue codes at `/admin/invites`.

The same page sets the site name, footer source link, welcome copy, public
contact details and house rules, plus a favicon and mascot. Images can be PNG,
JPEG or GIF up to 2 MiB; Imvault serves them as PNG. The source link starts at
`IMVAULT_SOURCE_URL` and can be changed or hidden here. The footer links to
`/about`, which names the active administrators and moderators. Contact details
and rules are public, so keep private member information out of them.

The retention window takes hours (`24`) or a duration with a unit (`24h`, `7d`).
It must be positive; to stop anonymous uploads, turn them off. Changing it
**rewrites the deadline on existing anonymous uploads**, still measured from
each upload's arrival, so a shorter window removes older files on the reaper's
next pass.

Anonymous uploads are always public. There is no account to limit them to, and
the link the uploader gets would not open for them otherwise.

The default visibility shapes the site most: **private** suits a personal host,
**members** a group and **public** a public one. Existing uploads keep their
level when you change it.

The storage ceiling covers every account and anonymous uploads together. Use it
when the disk is the limit; per-account quotas are under `/admin/users`.

## Storage ceilings

The **instance ceiling** (`IMVAULT_MAX_TOTAL_BYTES`, or the field in instance
settings) caps every account's uploads and every anonymous upload combined.
Once it is reached, uploads are refused with a message saying so. It counts
stored originals, the "stored" figure on the dashboard.

The **per-account quota** is set for each account at `/admin/users`:

| | |
| --- | --- |
| **Total storage** | Everything the account holds. `0` means unlimited |
| **Largest single file** | `0` means the instance defaults apply |

The single-file limit replaces the defaults, so you can raise it for someone
you trust as well as lower it. The defaults are `IMVAULT_MAX_UPLOAD_BYTES` for
images and animations and `IMVAULT_MAX_VIDEO_BYTES` for clips.

New accounts get `IMVAULT_DEFAULT_QUOTA_BYTES`. Accounts created before quotas
existed stay unlimited. Quotas count original upload bytes, not thumbnails or
previews. Two simultaneous uploads cannot both claim the last bit of room, a
failed upload gives its bytes back, and deleting a file credits its owner.

## Concurrency

`IMVAULT_MAX_CONCURRENT_UPLOADS` caps how many uploads are processed at once
across all users. It defaults to one whatever the CPU count. Images are limited
to 24 megapixels before decoding; raise concurrency only if the host has memory
for several decodes at once.

Rate limits cap how many uploads each person starts. This setting caps the
total, which matters because each clip upload runs ffmpeg. A request that waits
ten seconds without a slot gets `503` with `Retry-After`. A browser shows the
message in place.

## Rate limiting

Each identity gets `IMVAULT_UPLOAD_BURST` uploads up front, refilling at
`IMVAULT_UPLOAD_RATE_PER_HOUR`. Over budget, uploads get `429` with
`Retry-After`. Signed-in uploads are counted per account and anonymous uploads
per client address. Reads are never limited. Limits live in memory and reset on
restart.

**Behind a reverse proxy**, every request comes from the proxy's address, so
anonymous uploads would share one budget. List the proxy in
`IMVAULT_TRUSTED_PROXIES` (`127.0.0.1/32,::1/128` on the same host) to read the
client from `X-Forwarded-For`. Imvault believes only entries appended by listed
proxies, and never reads `X-Real-IP`.

An IPv6 client is limited by its /64, so rotating addresses does not buy a fresh
budget. Each limiter tracks up to 10,000 callers and forgets the least recently
seen when full.

---
