# Automatic backups

The running server makes a verified, compressed backup every 24 hours. You do
not need cron or a timer. The first attempt runs one minute after startup; later
ones count from the last successful backup, across restarts. A failed attempt
retries after 15 minutes.

Backups go to `backups/` inside the data directory. Each snapshot directory holds
`manifest.json` and `snapshot.tar.zst` if `zstd` is on the service's `PATH`,
otherwise `snapshot.tar.gz`. If zstd is installed but fails, the backup reports
the error instead of falling back to gzip.

## Limits and scheduling

Set these in `/etc/conf.d/imvault` (OpenRC) or `/etc/imvault/imvault.env`
(systemd), then restart:

```sh
IMVAULT_BACKUP_INTERVAL="24h"
IMVAULT_BACKUP_KEEP="7"
IMVAULT_BACKUP_MAX_BYTES="10737418240"
IMVAULT_BACKUP_COPY_MAX_BYTES="5368709120"
```

- `IMVAULT_BACKUP_INTERVAL`: 1 hour to 365 days; `0` turns automatic backups off.
- `IMVAULT_BACKUP_KEEP`: 1 to 30 copies, default 7.
- `IMVAULT_BACKUP_MAX_BYTES`: total compressed archives kept, default 10 GiB.
- `IMVAULT_BACKUP_COPY_MAX_BYTES`: one uncompressed snapshot, default 5 GiB.
  Raise it if your collection is larger. It does not limit your library.

Old copies are removed by count and total size, but the newest snapshot is
always kept. A snapshot too large for the archive cap is rejected and nothing is
deleted. Manual backups and other apps' backups are never touched. A failed
backup leaves no snapshot and removes nothing.

While a backup runs, leave free space for one uncompressed snapshot plus its
archive.

Owners see status, destination, limits, the last success, the last error and
the next attempt on the instance settings page. Errors also go to the service
log. Backups are never served over the web. Service configuration and external
credentials are not included.

## Recovery

Copy completed snapshot directories to another machine and keep them private;
they contain passwords and private media. Local copies share the live disk's
fate.

For a restore drill, compare the archive's `sha256sum` with the hash in
`manifest.json`, then extract into a new private directory:

```sh
umask 077
mkdir /private/restore-drill
tar -xf /private/backups/SNAPSHOT/snapshot.tar.zst -C /private/restore-drill
```

Use `snapshot.tar.gz` for gzip copies; zstd copies need zstd to extract. Extract
only backups you trust, and never over a running data directory. Then restore:

```sh
imvault restore --input /private/restore-drill --output /private/imvault-restored
```

The [manual backup command and timers](operations.md#backups-and-restore-drills)
keep working alongside this. To use only one, disable the timer or cron job, or
set `IMVAULT_BACKUP_INTERVAL=0`. These limits apply only to automatic backups.
