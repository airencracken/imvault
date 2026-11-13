# Operations

## Log rotation

The OpenRC service writes to `/var/log/imvault.log`. Its installer and the
Gentoo ebuild include `/etc/logrotate.d/imvault`. To add the rule to an existing
source install:

```bash
sudo emerge --ask app-admin/logrotate       # Gentoo, if not already installed
sudo make install-logrotate
sudo logrotate --debug /etc/logrotate.d/imvault
```

The debug run changes nothing. Reinstalling keeps an existing rule, so your
edits survive. `DESTDIR` and `LOGROTATEDIR` support staged installs. If you set
`IMVAULT_LOG_FILE` in `/etc/conf.d/imvault`, change the path in the rule too.

The rule rotates daily, or above 10 MiB, and keeps 14 archives. The newest
archive stays uncompressed; older ones are gzipped. Size is checked only when
logrotate runs, so schedule it more often for busy logs.

Installing the rule does not schedule it. Gentoo's default `cron` USE flag
installs `/etc/cron.daily/logrotate`, which needs a running cron daemon. A
systemd host can use `logrotate.timer`.

The rule uses `copytruncate`, because Imvault cannot reopen its log file. A few
lines can be lost during rotation; see the
[logrotate manual](https://github.com/logrotate/logrotate/blob/main/logrotate.8.in).

The systemd service logs to the journal and needs no rule.

## Backups and restore drills

Back up these three together:

| Path | Contents |
| --- | --- |
| `<data>/imvault.db` | Accounts and every record of what was uploaded |
| `<data>/objects/` or the S3 bucket and prefix | Uploaded files and renditions |
| `<data>/secret.key` | The key that decrypts two-factor secrets |

**Keep `secret.key` with the database.** Restore the database without it and
everything looks fine until people with two-factor authentication try to sign
in: their TOTP secrets cannot be decrypted, and startup gives no warning. Each
affected person then needs a recovery code, or an administrator must clear their
second factor at `/admin/users`. Neither helps a sole administrator.

To keep the key off disk, set `IMVAULT_SECRET_KEY` from your secrets store.
Changing or losing that value has the same effect as losing the file.

The `backup` command works while the server runs. Browsing and uploads continue.
For the native packages, create a private destination once, then run:

```sh
sudo install -d -o imvault -g imvault -m 0700 /var/backups/imvault
sudo /usr/bin/imvault backup --output-dir /var/backups/imvault
```

`--output-dir` writes a uniquely named snapshot and keeps the newest seven
scheduled snapshots for this instance. `--output NEW_DIRECTORY` writes to a
directory of your choosing and never rotates. Run as root, the command reads
the installed service configuration and switches to the service account. It
reads literal settings and does not run shell commands. For an instance without
installed service files, run it as the service account with the full service
environment.

### Scheduled backups

Pick one scheduler. Neither is enabled at install. Create the destination above
first.

On systemd, the packages and `make install-systemd` ship a daily timer:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now imvault-backup.timer
systemctl list-timers imvault-backup.timer
journalctl -u imvault-backup.service
```

The timer fires around 03:00, with up to 30 minutes of random delay, and catches
up after a missed run. The unit reads `/etc/imvault/imvault.env`. For custom
data paths, add them to `ReadWritePaths` in a unit override.

On OpenRC or another cron host, run `make install-backup-cron PREFIX=/usr`, or
copy `contrib/cron/imvault-backup` to `/etc/cron.d/imvault-backup` with mode
0644. It runs daily at 03:17 as root and mails failures to root, so configure
local mail. Native packages ship it as documentation only. With a different
install prefix, fix the binary path.

Both use `--keep 7`. Change the cron line or override the systemd unit's
`ExecStart` for another count; `--keep 0` keeps everything. Old snapshots are
removed only after the new one verifies, and only completed scheduled snapshots
of this database. Manual backups, other instances' snapshots, symlinks and
unrecognized directories are left alone. Moving the database starts a new
retention group, so clear out the old snapshots yourself.

Snapshots hold passwords, tokens and private media; keep the destination
private. Leave room for one more full snapshot, watch the command's exit
status, keep an off-host copy, and test recovery now and then:

```sh
imvault restore --input BACKUP --output NEW_DIRECTORY
```

A snapshot holds the database, encryption key, media and a checksum manifest.
Restore refuses an existing destination. See
[Storage](storage.md#backup-and-restore) for restoring service settings and
moving back to S3. Avoid manual database writes and outside bucket changes
during a backup. A plain file copy of a live database does not give the same
guarantees.

The running server also makes its own compressed backups; see
[automatic backups](automatic-backups.md).

## What maintains itself

Every `IMVAULT_CLEANUP_INTERVAL` (default 15 minutes), a background worker:

- deletes expired anonymous uploads and their objects;
- purges Trash items past their 30 days;
- prunes tags that label nothing;
- deletes expired sessions, API keys and one-time tokens;
- removes delivered mail older than seven days.

After downtime, the first pass catches up. Failed object deletions are retried
once storage is reachable. Old renditions kept by a manual rebuild are not
collected; see [rebuilding thumbnails](storage.md#rebuilding-thumbnails-and-posters).

## The outbound mail queue

Mail is queued in the database before delivery, so a down relay delays mail
instead of losing it. Retries back off from a minute up to a few hours, until
`IMVAULT_MAIL_MAX_ATTEMPTS` (default 5).

`/admin/mail` lists queued, recently delivered and failed messages. A message
that ran out of attempts stays there with the relay's error, ready to requeue or
discard.

A password reset that was never delivered still used up its token; the person
can request another. When someone says a reset link "isn't working", check
`/admin/mail` first.

## Recalculating storage usage

Storage totals are running counts. A killed upload can leave them wrong. After
an unclean shutdown, choose **Recalculate storage usage** on `/admin`. It
recounts every account and reports how many it corrected.

## Upgrading

Migrations are built into the binary and run once each at startup. To upgrade,
back up, then stop the service, replace the binary and start it:

```bash
sudo imvault backup --output-dir /var/backups/imvault
systemctl stop imvault
sudo make install       # or install the new release; see releases.md
systemctl start imvault
journalctl -u imvault -n 5
```

Take the backup with the binary you are replacing. Migrations do not run
backwards. [Binary releases](releases.md) covers package and archive upgrades.

### Going back to an older binary

A binary that finds migrations it does not know refuses to start, and so does
every command that opens the database:

```text
database /var/lib/imvault/imvault.db was upgraded by a newer Imvault: it has 1
migration(s) this binary does not know (029_example.sql). This is an older
Imvault binary running against a newer database, and it will not open it.
```

Install the newer Imvault again, or restore the backup taken before the upgrade
and run the matching older binary against it.

`backup` never migrates, and runs only against a database at its own schema
version, so a pre-upgrade backup stays usable for going back. If it says the
database is older than the binary, back up with the version that last ran the
database, or start the new server once and back up after. `restore` accepts
older backups, which the server migrates on next start, and refuses newer ones.
Other maintenance commands migrate first, like the server.

## Troubleshooting

### The temporary directory

Uploads larger than 8 MiB spill to the temporary directory, and one request can
carry twenty files. Keep that directory off the filesystem holding the database,
so a full spill cannot take the database with it.

The container image sets `TMPDIR=/tmp`, and both Compose files mount a 512 MiB
tmpfs there. An upload that does not fit fails; the instance keeps running.
Raise the size if you accept large multi-file uploads and have the memory, or
lower `IMVAULT_MAX_UPLOAD_BYTES` and `IMVAULT_MAX_VIDEO_BYTES`.

Packages do not set `TMPDIR`, so uploads spill to the system temporary
directory. Point it elsewhere if that shares a small disk with the data.

### Reading the log

Log lines are logfmt, so `grep` goes a long way:

```bash
journalctl -u imvault -n 100 | grep 'level=ERROR'
tail -f /var/log/imvault.log | grep 'level=WARN'
```

| Symptom | Likely cause |
| --- | --- |
| `429` on upload | Rate limit: `IMVAULT_UPLOAD_RATE_PER_HOUR` and `IMVAULT_UPLOAD_BURST`. The response has `Retry-After`. |
| `429` on sign-in | `IMVAULT_LOGIN_RATE_PER_HOUR`, counted per address and username. |
| "storage quota exceeded" | The account's quota, set at `/admin/users`. Zero means unlimited. |
| Upload refused as too large | `IMVAULT_MAX_UPLOAD_BYTES` for images, `IMVAULT_MAX_VIDEO_BYTES` for clips. |
| Clips have grey placeholder posters | ffmpeg is not on `PATH`. The startup log says so. |
| Clip rejected for length | `IMVAULT_MAX_VIDEO_DURATION`, checked only when ffprobe is available. |
| Nobody can sign in with two-factor | The encryption key changed or was lost. See [backups](#backups-and-restore-drills). |
| `module requires go >= 1.26` when building | Your Go toolchain is too old. |
| Service fails after moving the data directory | `ProtectSystem=strict` makes other paths read-only. Add the new path to `ReadWritePaths`. |
| Anonymous uploads vanish early | The retention window. Change it at `/admin/settings` or with `IMVAULT_ANONYMOUS_TTL`. |
| Mail queued but never arrives | The relay is unreachable. `/admin/mail` shows its error. |
| "this instance is full" | The instance ceiling, `IMVAULT_MAX_TOTAL_BYTES`. Delete something or raise it. |
| `503` on upload | All upload slots are busy: `IMVAULT_MAX_CONCURRENT_UPLOADS`. The response has `Retry-After`. |
| A large multi-file upload fails while others work | The [temporary directory](#the-temporary-directory) is too small. |

For a bug report, include the log line and a run with `IMVAULT_LOG_LEVEL=debug`.

## Trash retention

Deleting your own upload moves it to private Trash for 30 days. Restoring is
refused once the deadline passes, and the next cleanup pass purges it. Trash
counts toward storage limits until then and is included in backups. Removals by
staff and account deletion are permanent. See
[organizing your library](library.md#trash-and-restore).
