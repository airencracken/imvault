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

OpenRC redirects stdout and stderr to an open file descriptor, and Imvault has
no signal handler to reopen it. The rule uses `copytruncate` to preserve that
descriptor, ownership, and permissions without a service restart. A few lines
can be lost between copying and truncating; this is the documented
[logrotate tradeoff](https://github.com/logrotate/logrotate/blob/main/logrotate.8.in).

The supplied systemd service logs to the journal, whose retention is managed
by journald. It does not need this file-based rule.

## Backups and restore drills

Imvault needs the database, media, and encryption key together:

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

Use the verified backup command while Imvault 0.13 or later is running. The
snapshot pins stored objects against deletion until copying finishes; browsing
and uploads continue. Stop older servers before backing up: they do not support
this coordination and the command refuses a live backup under 0.12.

For the native packages, create a private destination once and run:

```sh
sudo install -d -o imvault -g imvault -m 0700 /var/backups/imvault
sudo /usr/bin/imvault backup --output-dir /var/backups/imvault
```

`--output-dir` creates a uniquely named snapshot below an existing directory.
`--output NEW_DIRECTORY` chooses an exact destination instead. When run as root,
the CLI reads the installed service configuration and switches to its account,
including database, storage and encryption settings. It reads literal settings;
it does not evaluate shell commands. For an uninstalled instance, run as its
account with the complete service environment.

### Scheduled backups

Choose one scheduler; both are optional and neither is enabled by installation.
Create the private destination above before enabling it.

On systemd, the packages and `make install-systemd` ship a daily timer:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now imvault-backup.timer
systemctl list-timers imvault-backup.timer
journalctl -u imvault-backup.service
```

The backup unit uses `/etc/imvault/imvault.env`. For custom data paths, add those
paths to its `ReadWritePaths` in a unit override. Its default runs around 03:00
with up to 30 minutes of random delay and catches a missed run after startup.

On OpenRC or another cron host, copy `contrib/cron/imvault-backup` to
`/etc/cron.d/imvault-backup`, mode 0644, or use `make install-backup-cron
PREFIX=/usr`. Native packages carry this example in their documentation, not as
an active cron job. It runs daily at 03:17 as root; the CLI switches to the
service account. Configure local cron mail to see failures. A source install
with a different prefix should adjust the binary path.

Backups are retained until you remove them. Check free space and command status,
keep a copy on another machine, and periodically test recovery:

```sh
imvault restore --input BACKUP --output NEW_DIRECTORY
```

The snapshot contains the database, encryption key, media and checksum manifest,
and appears only after verification. Restore refuses an existing destination.
See [Storage](storage.md#backup-and-restore) for restoring service settings and
moving a restored collection back to S3. Avoid manual database writes or outside
bucket changes during a backup. A plain copy of a live WAL database and media
does not provide these guarantees.

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

**Back up first,** with the binary you are about to replace. A migration that
turns out to be unwelcome is much easier to undo with a copy of the database
than without one. Migrations do not run backwards.

### Going back to an older binary

Each migration is recorded by name in the database. A binary that finds a
migration it does not have refuses to start, and so does every command that
opens the database:

```text
database /var/lib/imvault/imvault.db was upgraded by a newer Imvault: it has 1
migration(s) this binary does not know (029_example.sql). This is an older
Imvault binary running against a newer database, and it will not open it.
```

It means an older binary is running against a database a newer Imvault has
already upgraded. Earlier versions carried on regardless, and an older binary
reading and writing tables whose meaning has changed can damage them without
any error at the time. Install the newer Imvault again, or, to go back, restore
the backup taken before the upgrade and run the matching older binary against
the restored directory.

`backup` goes further: it does not migrate the database at all, and it only
runs against a database at its own binary's schema. Migrating first would turn
the backup you take before an upgrade into a copy of the upgraded database,
which is no use for going back. If the backup reports that the database
predates the binary, either take it with the version that last ran the
database, or start the new server once and back up afterwards. `restore`
refuses a backup taken by a newer Imvault, and accepts an older one, which the
server migrates on its next start. The other maintenance commands change the
collection, and they migrate first just as the server does.

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
