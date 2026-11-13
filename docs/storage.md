# Storage and maintenance

Files live on local disk by default. With S3 storage, originals, thumbnails,
previews and metadata-free copies go to a private bucket. The database,
encryption key and video scratch files stay on the Imvault host.

## Choosing storage

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `IMVAULT_STORAGE` | `disk` | `disk` or `s3` |
| `IMVAULT_OBJECTS_DIR` | `<data>/objects` | Object directory for disk storage |
| `IMVAULT_S3_BUCKET` | unset | Required for S3; an existing private bucket |
| `IMVAULT_S3_REGION` | `us-east-1` | Signing region; `auto` for R2 |
| `IMVAULT_S3_ENDPOINT` | unset | Custom HTTP(S) origin; omit for AWS |
| `IMVAULT_S3_PREFIX` | unset | Key prefix inside the bucket |
| `IMVAULT_S3_PATH_STYLE` | `false` | `true` for endpoints that need `/bucket/key` addressing |
| `IMVAULT_S3_ACCESS_KEY` | unset | Access key; set with the secret key |
| `IMVAULT_S3_SECRET_KEY` | unset | Secret key |
| `IMVAULT_S3_SESSION_TOKEN` | unset | Temporary-credential token |
| `IMVAULT_S3_TIMEOUT` | `2m` | Timeout for each S3 request, body included |

Without explicit keys, Imvault uses the AWS SDK's credential chain:
`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, profiles and instance roles. Under
the Bubblewrap sandbox, expose a profile or credentials file with `--read-file`;
see [Linux confinement](sandbox.md#what-the-server-can-access). A custom
endpoint works with R2 and self-hosted S3-compatible stores. Use HTTPS for
remote endpoints; certificates are always verified.

```bash
IMVAULT_STORAGE="s3"
IMVAULT_S3_ENDPOINT="https://objects.example.com"
IMVAULT_S3_REGION="us-east-1"
IMVAULT_S3_BUCKET="photos"
IMVAULT_S3_PREFIX="imvault"
IMVAULT_S3_PATH_STYLE="true"
```

Put credentials in the service's protected environment or an AWS credential
provider. On OpenRC that is `/etc/conf.d/imvault`; restrict its permissions.

Grant read, write and delete on objects under the prefix, plus multipart
uploads. On AWS: `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject` and
`s3:AbortMultipartUpload`, plus `s3:ListBucket` scoped to the prefix so missing
objects return `404`. Create the bucket yourself. Imvault never creates buckets,
changes bucket policy or sets public ACLs.

Give each instance its own prefix, and run one server per database. Every
download goes through Imvault, so visibility and EXIF rules apply. Viewers never
see bucket URLs or credentials.

Uploads spool to temporary files first; those above 64 MiB use multipart
requests. Leave room in `TMPDIR` for uploads and ffmpeg's working copies.
Requests make up to three attempts. Set the bucket to expire incomplete
multipart uploads, which a killed process can leave behind.

S3 compatibility varies. Before moving a live collection, test uploads,
playback, deletion and backup against your endpoint. See the
[AWS endpoint guide](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-endpoints.html)
and the [R2 compatibility table](https://developers.cloudflare.com/r2/api/s3/api/).

To check an endpoint from a source checkout, set `IMVAULT_TEST_S3_BUCKET` and
the matching `IMVAULT_TEST_S3_ENDPOINT`, region, path-style and credential
variables, then run:

```bash
go test ./internal/storage -run TestS3ExternalEndpoint -count=1 -v
```

It writes, reads and deletes a small object under a random prefix. Use a test
bucket; versioning may keep the deleted object.

## Running maintenance

Run maintenance commands as the service account, with the service's
`IMVAULT_*` settings. The CLI does not source `/etc/conf.d/imvault`. Run as root,
`backup`, `migrate-storage`, `rebuild-thumbnails` and `refresh-metadata` switch
to the account named by the installed systemd or OpenRC service, and refuse to
run if there is none.

Stop the server before `migrate-storage` or `rebuild-thumbnails`; a lock refuses
to run them alongside it. `backup` can run while the server is up. Avoid manual
database writes and other bucket writers during maintenance.

On a Gentoo disk install (the package uses `/usr/bin`; `make install` uses
`/usr/local/bin`):

```bash
sudo rc-service imvault stop
sudo -u imvault env IMVAULT_DATA_DIR=/var/lib/imvault \
  /usr/bin/imvault rebuild-thumbnails --videos-only
sudo rc-service imvault start
```

For S3 or customized installs, pass the full service configuration too. If the
command fails, check its exit status before restarting.

## Moving a collection

`imvault migrate-storage` copies from the current `IMVAULT_*` storage to an
`IMVAULT_DEST_*` configuration. Each storage variable above has a destination
twin, such as `IMVAULT_DEST_S3_BUCKET`, including its own credentials. Disk to
S3, S3 to disk and S3 to S3 all work.

With the server stopped and its environment loaded:

```bash
export IMVAULT_DEST_STORAGE="s3"
export IMVAULT_DEST_S3_BUCKET="photos"
export IMVAULT_DEST_S3_REGION="us-east-1"
export IMVAULT_DEST_S3_PREFIX="imvault"
# Add the destination endpoint and credentials as needed.
imvault migrate-storage
```

The command prints progress, reads back every copied object and checks its
SHA-256 and size against the database. If interrupted, run it again; matching
objects are skipped. A conflicting object at the destination stops the run
without being overwritten.

File IDs, links and settings carry over. The source is left in place and the
service configuration is unchanged. After a clean run, switch the service's
storage settings to the destination and restart. Keep the source and a backup
until you have checked the new setup.

To copy back to disk, set `IMVAULT_DEST_STORAGE=disk` and
`IMVAULT_DEST_OBJECTS_DIR` to a new directory.

## Rebuilding thumbnails and posters

`imvault rebuild-thumbnails` remakes thumbnails and previews from the
originals. Add `--videos-only` to fix posters for clips uploaded before ffmpeg
was installed; clips need both ffmpeg and ffprobe.

Originals, descriptions, tags, links and visibility are unchanged. Clip
dimensions and durations are re-measured. Clips longer than today's duration
limit are kept. Browsers pick up the new posters through versioned URLs.

A failure leaves that file's current renditions in place and exits nonzero.
Rerunning is safe. Replaced renditions are deleted unless another record still
uses them.

## Backup and restore

`imvault backup --output DIRECTORY` creates a new, self-contained directory:

```text
snapshot/
  manifest.json
  imvault.db
  secret.key
  objects/
```

Run it with the Imvault version that last ran the database; see
[going back to an older binary](operations.md#going-back-to-an-older-binary).
The command copies the database and every referenced original and rendition,
checks hashes and sizes, and confirms the encryption key decrypts the stored
two-factor secrets. That includes a key set through `IMVAULT_SECRET_KEY`. A
missing or damaged original fails the backup. Missing thumbnails, previews and
metadata-free copies are reported and skipped, since they can be rebuilt.

The directory appears only after every check passes, with mode `0700` and the
database, key and manifest at `0600`. It holds credentials and originals with
their metadata; protect it. Checksums catch corruption but do not prove a backup
is trustworthy. Service configuration and external credentials are not
included. The output needs room for all media, even when the source is S3.

Restore to a **new** local directory:

```bash
imvault restore --input /backups/snapshot --output /var/lib/imvault-restored
```

Restore checks the database, object inventory, checksums and encryption key
before publishing the result. It refuses backups from a newer Imvault and will
not replace an existing directory. It needs no S3 access or service
configuration, so you can run restore drills on another machine.

To switch over, stop the service and point it at the restored directory with
`IMVAULT_STORAGE=disk`. Set or clear `IMVAULT_DB`, `IMVAULT_OBJECTS_DIR`,
`IMVAULT_SECRET_KEY_FILE` and `IMVAULT_SECRET_KEY` to match. Make the service
account the owner, start the service, and check sign-in and media. Run
`migrate-storage` afterwards to move the collection back to S3.
