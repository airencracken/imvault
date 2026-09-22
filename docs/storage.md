# Storage and maintenance

Local disk is the default. An optional S3 backend keeps originals, thumbnails,
previews, and metadata-free copies in a private bucket. The database, encryption
key, and video-processing scratch files stay on the imvault host.

## Choosing storage

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `IMVAULT_STORAGE` | `disk` | `disk` or `s3` |
| `IMVAULT_OBJECTS_DIR` | `<data>/objects` | Object directory for disk storage |
| `IMVAULT_S3_BUCKET` | unset | Required for S3; an existing private bucket |
| `IMVAULT_S3_REGION` | `us-east-1` | Signing region; use `auto` for R2 |
| `IMVAULT_S3_ENDPOINT` | unset | Custom HTTP(S) origin; omit for AWS |
| `IMVAULT_S3_PREFIX` | unset | Optional directory-like prefix within the bucket |
| `IMVAULT_S3_PATH_STYLE` | `false` | Enable for endpoints that need `/bucket/key` addressing |
| `IMVAULT_S3_ACCESS_KEY` | unset | Explicit access key; pair with the secret key |
| `IMVAULT_S3_SECRET_KEY` | unset | Explicit secret key |
| `IMVAULT_S3_SESSION_TOKEN` | unset | Optional temporary-credential token |
| `IMVAULT_S3_TIMEOUT` | `2m` | Timeout for each S3 HTTP request, including its body |

Without explicit credentials, S3 uses the AWS SDK's normal credential chain,
including `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, profiles, and instance
roles. A custom endpoint supports S3-compatible services such as R2 and
self-hosted object stores. Use HTTPS for remote endpoints. Certificate
verification is always enabled.

Example settings for an existing private S3-compatible bucket:

```bash
IMVAULT_STORAGE=s3
IMVAULT_S3_ENDPOINT=https://objects.example.com
IMVAULT_S3_REGION=us-east-1
IMVAULT_S3_BUCKET=photos
IMVAULT_S3_PREFIX=imvault
IMVAULT_S3_PATH_STYLE=true
```

Supply credentials through your service's protected environment or an AWS
credential provider. Never put them into a repository or a public URL.
On OpenRC these settings belong in `/etc/conf.d/imvault`; restrict its
permissions if it contains credentials. No changes to Caddy are needed.

Give imvault permission to read, write, and delete objects beneath its prefix,
and to start, upload, complete, and abort multipart uploads. On AWS this normally
means `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject`, and
`s3:AbortMultipartUpload`, plus `s3:ListBucket` scoped to the prefix so missing
objects produce a distinguishable `404`. The bucket must already exist. imvault
does not create buckets, change bucket policy, or set public ACLs.

Use a dedicated prefix for each instance. One running server is allowed per
database. All readers fetch bytes through imvault, so file visibility and EXIF
policy apply before an object is served. Bucket URLs and credentials are never
exposed to viewers.

Video seeking uses bounded ranged reads. Uploads are spooled to temporary files
for signing and retries; uploads above 64 MiB use multipart requests. Allow room
in `TMPDIR` for uploads and ffmpeg's working copies. Requests retry up to three
attempts; failed object deletions remain tracked for the cleanup worker to retry.
An interrupted multipart upload is aborted when possible; configure the bucket
to expire incomplete multipart uploads left by a killed process.

The automated suite exercises the real AWS SDK against a local S3 protocol
fixture, including ranged reads, multipart writes, cancellation, and access
errors. Hosted AWS/R2 credentials are not required by the suite. Before moving
a live collection, test uploads, playback, deletion, and backup against your
chosen endpoint. S3 compatibility varies by implementation; see the
[AWS endpoint guide](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-endpoints.html)
and [R2 compatibility table](https://developers.cloudflare.com/r2/api/s3/api/).

There is also an opt-in endpoint test. Set `IMVAULT_TEST_S3_BUCKET` and the
corresponding `IMVAULT_TEST_S3_ENDPOINT`, region, path-style and credential
variables, then run:

```bash
go test ./internal/storage -run TestS3ExternalEndpoint -count=1 -v
```

It writes a small object under a random test prefix, checks streaming and
seeking, then deletes it. Bucket versioning may retain the deleted version.
Use a test bucket; without these settings the provider test is explicitly skipped.

## Running maintenance

Run commands as the service's OS user, with the same `IMVAULT_*` settings as the
service. The CLI does not automatically source `/etc/conf.d/imvault`.
`migrate-storage`, `rebuild-thumbnails`, and `backup` require the server to be
stopped. An instance lock rejects concurrent servers and maintenance; locks
are released automatically if the process exits. Older imvault binaries do not
honor this lock, so stop any older service too. Avoid manual database writes or
other writers to the bucket during maintenance.

For a default disk installation on Gentoo:

```bash
sudo rc-service imvault stop
sudo -u imvault env IMVAULT_DATA_DIR=/var/lib/imvault \
  /usr/local/bin/imvault rebuild-thumbnails --videos-only
sudo rc-service imvault start
```

For customized or S3 deployments, pass or export the full service configuration
as well. Choose `/usr/bin/imvault` for an ebuild or `PREFIX=/usr` install. Check
the command's exit status before restarting after a failure.

## Moving a collection

`imvault migrate-storage` reads the current `IMVAULT_*` backend and copies to a
separate `IMVAULT_DEST_*` configuration. Every storage variable in the table has
a destination equivalent, for example `IMVAULT_DEST_S3_BUCKET`. Destination
credentials can differ from the source; otherwise the normal AWS credential
chain applies. This supports disk to S3, S3 to disk, and S3 to S3.

With the server stopped and its environment loaded, for example:

```bash
export IMVAULT_DEST_STORAGE=s3
export IMVAULT_DEST_S3_BUCKET=photos
export IMVAULT_DEST_S3_REGION=us-east-1
export IMVAULT_DEST_S3_PREFIX=imvault
# Configure destination endpoint and protected credentials as needed.
imvault migrate-storage
```

The command prints progress, reads back destination bytes, and verifies their
SHA-256 hashes and sizes. It also checks originals against the database's content
hashes. Rerun the same command after interruption: matching destination objects
are verified and skipped. A conflicting destination object stops the command
without overwriting it; select a fresh destination or investigate the conflict.

Migration preserves object keys, file IDs, links, and account settings. It does
not delete source objects or edit service configuration. After a successful
run, change the service's `IMVAULT_*` storage settings to match the destination
and restart. Keep the source and a backup until you have checked the new setup.

To copy back to disk, set `IMVAULT_DEST_STORAGE=disk` and
`IMVAULT_DEST_OBJECTS_DIR` to a new object directory.

## Rebuilding thumbnails and posters

`imvault rebuild-thumbnails` rebuilds thumbnails and previews from unique
originals. Add `--videos-only` to repair posters for clips uploaded before
ffmpeg was installed. Both ffmpeg and ffprobe must be available for clips.

Original bytes, descriptions, ownership, links, tags, and visibility are
preserved. The command updates dimensions and duration measurements for clips,
and publishes new rendition keys only after their bytes have been verified.
Updated pages use versioned thumbnail URLs so browsers pick up repaired posters.
Identical uploads share the rebuilt renditions. Previously accepted clips are
not rejected because today's upload duration limit is lower.

Failures leave the affected file's current rendition keys intact and return a
nonzero status. Earlier successful items remain rebuilt, and rerunning is safe.
Old rendition objects are retained; repeated repairs can leave unreferenced
derived objects in storage. Verified backups include the current objects named
by the database, not these older copies.

## Backup and restore

`imvault backup --output DIRECTORY` creates a self-contained directory:

```text
snapshot/
  manifest.json
  imvault.db
  secret.key
  objects/
```

The directory must not already exist. The command takes a SQLite snapshot,
copies every referenced original and rendition from the active disk or S3
backend, and verifies hashes and sizes. It includes the active encryption key,
including when supplied by `IMVAULT_SECRET_KEY`, and checks that it can decrypt
stored two-factor secrets. Missing or damaged media prevents a successful backup.

The completed directory is published only after all checks pass. Its permissions
are `0700`, with the database, key, and manifest `0600`. Keep the whole snapshot
protected: it contains account credentials and originals with their metadata.
Checksums detect corruption; they do not authenticate an untrusted backup.
Service configuration and external credentials are not included; preserve those
separately. The output needs space for all media, even with an S3 source.

Restore to a **new** local data directory:

```bash
imvault restore --input /backups/snapshot --output /var/lib/imvault-restored
```

Restore verifies the database, foreign keys, object inventory, file checksums,
and encryption key before publishing the result. It refuses to replace an
existing directory. It works without the old S3 connection or service
configuration, making it suitable for a restore drill on another machine.

After checking the result, stop the service and configure it to use the restored
directory with `IMVAULT_STORAGE=disk`. Set or clear overrides for `IMVAULT_DB`,
`IMVAULT_OBJECTS_DIR`, `IMVAULT_SECRET_KEY_FILE`, and `IMVAULT_SECRET_KEY` so they
refer to the restored database, objects, and key. Ensure the service user owns
the restored directory. Start the service and verify sign-in and media access.
Use `migrate-storage` afterward if the restored collection should live in S3.

Keep a copy on another machine and periodically perform a restore drill. A live
SQLite backup alone is consistent for the database, but independently copying
media while deletions continue can leave it incomplete. The stopped-instance
requirement covers both halves together; see
[SQLite's backup documentation](https://www.sqlite.org/backup.html).
