# Automatic backups

Starting with Imvault 0.19.0, the running service creates a verified,
compressed backup every 24 hours. No cron job or timer is required. The first
attempt starts one minute after startup; later attempts use the last successful
backup time, including across restarts. Failed attempts retry after 15 minutes.
Shutdown cancels the worker and joins it before closing the database.

Backups live in the private `backups/` subdirectory of the configured data
directory. Each completed snapshot directory contains `manifest.json` and
`snapshot.tar.zst` when `zstd` is on the service's PATH, otherwise
`snapshot.tar.gz`. Gzip is built into the app. Install zstd to use it in native
or Bubblewrap services. A zstd execution failure is reported rather than
silently substituting another compressor. Compression uses one worker thread.

## Limits and scheduling

Set these in `/etc/conf.d/imvault` for OpenRC or `/etc/imvault/imvault.env`
for systemd, then restart the service:

```sh
IMVAULT_BACKUP_INTERVAL=24h
IMVAULT_BACKUP_KEEP=7
IMVAULT_BACKUP_MAX_BYTES=10737418240
IMVAULT_BACKUP_COPY_MAX_BYTES=5368709120
```

- Interval: 1 hour through 365 days; `0` disables automatic backups.
- Copy count: 1 through 30, default 7.
- Retained compressed archive data: default 10 GiB per app.
- One uncompressed snapshot: default 5 GiB, checked before database copying
  and while copying additional files. Oversized collections require a larger
  explicit copy cap.

Rotation applies both count and archive byte limits and always keeps the
snapshot just completed, even if the system clock moved backwards. A snapshot
that cannot fit the archive cap is rejected without deleting existing backups.
Manual backups, other apps' backups and pre-migration snapshots are untouched.
A failed copy or compression leaves no completed snapshot and does not rotate
older copies. After a crash, the next running service removes its reserved
interrupted staging directories before creating another copy.

The retained archive cap excludes small manifests and scheduling state. During
a backup, allow additional disk space for one uncompressed snapshot and its
new compressed archive; rotation happens after the new snapshot is complete.
The copy cap is not a cap on the live app's storage. SQLite size is checked
before snapshotting and the finished snapshot is checked again.

Owners see enabled status, destination, caps, latest successful backup, latest
error and next attempt on the existing instance settings page. Errors also go
to the service log. Backup status and files are never public web routes.
Automatic backups do not include service configuration or outside credentials;
preserve those separately.

## Recovery

Keep a protected copy of completed snapshot directories on another machine.
Local backups protect against mistakes, but share the live disk's failure
risk. They contain passwords and private data.

For a restore drill, compare the archive's `sha256sum` with the hash in
`manifest.json`, create a new private extraction directory, and extract there:

```sh
umask 077
mkdir /private/restore-drill
tar -xf /private/backups/SNAPSHOT/snapshot.tar.zst -C /private/restore-drill
```

Use `snapshot.tar.gz` for gzip copies. Extraction of zstd copies needs zstd
installed. Extract only your trusted backups, and never over a running data
directory.

The snapshot includes the consistent SQLite database, referenced originals
and renditions from disk or S3, and the active encryption key. Hashes and
two-factor secret decryption are checked using the existing verified backup
implementation. Refetchable missing renditions may be skipped, as with manual
backups. Restore the extracted directory with the existing command:

```sh
imvault restore --input /private/restore-drill --output /private/imvault-restored
```

The existing optional cron/timer and manual backups still work independently.
Disable that timer or cron job if you want only the built-in schedule, or set
`IMVAULT_BACKUP_INTERVAL=0` if you prefer the external scheduler. These limits
apply only to the built-in schedule, not those separate backup destinations.
