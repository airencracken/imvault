# Operations

Keeping an instance running: what to back up, what maintains itself, and what
to look at when something is wrong.

## Log rotation

The OpenRC service writes to `/var/log/imvault.log` by default. Its installer
and the Gentoo ebuild include `/etc/logrotate.d/imvault`. To add the rule to an
existing source install:

```bash
sudo emerge --ask app-admin/logrotate       # Gentoo, if not already installed
sudo make install-logrotate
sudo logrotate --debug /etc/logrotate.d/imvault
```

The debug check does not change logs or rotation state. Both `install-logrotate`
and `install-openrc` preserve an existing rule, including symlinks, so your
local changes survive reinstalling. `DESTDIR` and `LOGROTATEDIR` support staged
installs. If you change `IMVAULT_LOG_FILE` in `/etc/conf.d/imvault`, change the
path inside the rule to match.

The rule rotates daily, or when the log exceeds 10 MiB, and keeps 14 numbered
archives. The latest archive stays uncompressed; older archives use gzip.
Missing and empty logs are ignored. Size is checked **when logrotate runs**;
10 MiB is not a hard size cap. Run it more often if that matters for your volume.

Ensure logrotate is scheduled. Gentoo's default `cron` USE flag installs
`/etc/cron.daily/logrotate`, which needs a running cron daemon configured to run
the daily jobs. A systemd host can use `logrotate.timer`. Installing the rule
alone does not schedule it.

OpenRC redirects stdout and stderr to an open file descriptor, and imvault has
no signal handler to reopen it. The rule uses `copytruncate` to preserve that
descriptor, ownership, and permissions without a service restart. A few lines
can be lost between copying and truncating; this is the documented
[logrotate tradeoff](https://github.com/logrotate/logrotate/blob/main/logrotate.8.in).

The supplied systemd service logs to the journal, whose retention is managed
by journald. It does not need this file-based rule.

## Backups and restore drills

imvault needs the database, media, and encryption key together:

| Path | What it is |
| --- | --- |
| `<data>/imvault.db` | Accounts, metadata, and every record of what was uploaded |
| `<data>/objects/` or the configured S3 bucket/prefix | The uploaded bytes and renditions |
| `<data>/secret.key` | The key that decrypts two-factor secrets |

**Back up `secret.key` alongside the database.** They are two halves of the same
thing, and restoring one without the other has a specific, quiet failure mode:
everything looks fine, every account still exists, and then anybody with
two-factor authentication enabled cannot sign in, because their TOTP secret
cannot be decrypted. There is no error at startup to warn you.

Recovery from a lost key is per account: either the person uses one of their
recovery codes, or an administrator clears their second factor from
`/admin/users`. Neither is possible if the account is the only administrator.

If you would rather not keep the key on disk at all, set `IMVAULT_SECRET_KEY`
and supply it from wherever you already keep secrets. Note that changing or
losing this value has the same effect as losing the file.

Use the verified backup command with the service stopped. For a default disk
installation on Gentoo:

```bash
sudo install -d -o imvault -g imvault -m 0700 /var/backups/imvault
sudo rc-service imvault stop
sudo -u imvault env IMVAULT_DATA_DIR=/var/lib/imvault \
  /usr/local/bin/imvault backup \
  --output "/var/backups/imvault/$(date +%Y%m%d-%H%M%S)"
sudo rc-service imvault start
```

Check the backup command's exit status. For custom settings or S3, provide the
complete service environment too; the CLI does not source `/etc/conf.d/imvault`.
On systemd hosts, use `systemctl stop/start imvault` around the same command.

The backup contains a SQLite snapshot, encryption key, media, and checksum
manifest. It is published only after verification. Copy it to another machine
and periodically run `imvault restore --input BACKUP --output NEW_DIRECTORY` to
test recovery. Restore refuses to overwrite an existing directory. See
[Storage](storage.md#backup-and-restore) for restoring service settings and
moving a restored collection back to S3.

A live SQLite snapshot protects database consistency, but copying media
separately while deletions continue can produce a backup with missing objects.
Stop the service for the whole backup, and do not copy a live WAL-mode database
with a plain filesystem copy.

## What maintains itself

A background worker runs every `IMVAULT_CLEANUP_INTERVAL` (15 minutes by
default), and does the following without being asked:

- deletes anonymous uploads whose retention window has closed, rows and objects
  together;
- prunes tags that no longer label anything;
- deletes expired sessions and expired API keys;
- deletes one-time tokens that have been used or have lapsed;
- removes delivered mail older than seven days.

If the service is stopped for a while, the first pass after it starts catches
up. Failed object deletions remain tracked until storage is reachable again.
Old renditions retained by a manual rebuild are not automatically collected;
see [thumbnail rebuilding](storage.md#rebuilding-thumbnails-and-posters).

## The outbound mail queue

Mail is written to a database table before delivery is attempted, so a relay
that is down delays a message rather than losing it. A worker retries with a
growing backoff, roughly 1m, 4m, 16m, 1h, and 4h, up to
`IMVAULT_MAIL_MAX_ATTEMPTS`.

`/admin/mail` shows what is queued, what has been delivered recently, and what
has given up. A message that exhausted its attempts is parked rather than
deleted, with the reason attached, and can be requeued or discarded by hand.

Worth knowing: **a password reset that was never delivered still consumed its
token.** The person can simply request another. If a reset link is being
reported as "not working", check `/admin/mail` before anything else.

## Recalculating storage usage

Each account's storage total is a running number, claimed when an upload
completes and released when a file is deleted. If the process is killed
part-way through an upload, that number can drift from the truth.

`/admin` has a **Recalculate storage usage** button that recomputes every
account's total from the files table. Running it is harmless and it reports how
many accounts it corrected. It is worth running after any unclean shutdown.

The equivalent by hand:

```sql
UPDATE users SET storage_used = (
  SELECT COALESCE(SUM(f.size), 0) FROM files f WHERE f.user_id = users.id
);
```

## Upgrading

Schema migrations are embedded in the binary and applied on startup, inside a
transaction, and each one is only applied once. Upgrading is therefore: stop the
service, replace the binary, start it.

```bash
systemctl stop imvault
make build && sudo make install       # or: tar -xzf dist/imvault-<version>.tar.gz
systemctl start imvault
journalctl -u imvault -n 5            # confirm it came up
```

**Back up first.** A migration that turns out to be unwelcome is much easier to
undo with a copy of the database than without one. Migrations do not run
backwards.

## Troubleshooting

### The temporary directory

Multipart bodies spill to a temporary directory once they pass 8 MiB, and one
request may carry twenty files, so a large multi-file upload needs room
somewhere. That somewhere should not be the data directory: putting a spill and
the SQLite database on the same filesystem means filling the first takes the
second with it.

The container image sets `TMPDIR=/tmp`, and both compose files give `/tmp` a
512 MiB tmpfs — bounded, and in memory rather than on the disk the database
lives on. The trade is explicit: if the spill does not fit, the upload fails
instead of the instance. Raise the size if you accept large multi-file uploads
and have the memory for it, or lower `IMVAULT_MAX_UPLOAD_BYTES` and
`IMVAULT_MAX_VIDEO_BYTES` to match what you actually want to accept.

Installed from a package rather than a container, nothing sets `TMPDIR`, so the
spill follows the system temporary directory. Point it at a separate filesystem
if the data directory is on a small one.

Start with the log. Every line is logfmt, so `grep` gets you a long way:

```bash
journalctl -u imvault -n 100 | grep 'level=ERROR'
tail -f /var/log/imvault.log | grep 'level=WARN'
```

| Symptom | Likely cause |
| --- | --- |
| `429` on upload | Rate limit. `IMVAULT_UPLOAD_RATE_PER_HOUR` and `IMVAULT_UPLOAD_BURST`; the response carries `Retry-After`. |
| `429` on sign-in | `IMVAULT_LOGIN_RATE_PER_HOUR`. Keyed by address and username, so one busy client cannot lock out everybody. |
| "storage quota exceeded" | The account's cap, settable per account at `/admin/users`. Zero means unlimited. |
| An upload is refused as too large | `IMVAULT_MAX_UPLOAD_BYTES` for images, `IMVAULT_MAX_VIDEO_BYTES` for clips. |
| Clips have grey placeholder posters | ffmpeg is not on `PATH`. The startup log says so explicitly. |
| A clip is rejected for its length | `IMVAULT_MAX_VIDEO_DURATION`, and only when ffprobe is available to measure it. |
| Nobody can sign in with two-factor | The encryption key changed or was lost. See the top of this page. |
| `module requires go >= 1.26` when building | The Go toolchain is too old. The dependencies need 1.26. |
| The service will not start after moving the data directory | `ProtectSystem=strict` makes everything but the data directory read-only. Add the new path to `ReadWritePaths` in the unit. |
| Anonymous uploads vanish early | That is the retention window. Edit it at `/admin/settings`, or see `IMVAULT_ANONYMOUS_TTL`. |
| Mail is queued but never arrives | The relay is unreachable. `/admin/mail` carries the exact error from the relay. |
| Uploads are refused as "this instance is full" | The instance ceiling, `IMVAULT_MAX_TOTAL_BYTES`. Delete something, or raise it. |
| Uploads are refused with a `503` | Every upload slot is busy. `IMVAULT_MAX_CONCURRENT_UPLOADS`; the response carries `Retry-After`. |
| A large multi-file upload fails while others work | The temporary directory is too small for it. See the note below. |

If you are reporting a bug, the log line and the output of `imvault` started
with `IMVAULT_LOG_LEVEL=debug` are the two things that make it reproducible.
