# Run Imvault with Docker

Published images are available at `ghcr.io/airencracken/imvault:v0.20.1` for
Linux amd64 and arm64. Docker selects the matching architecture. Images contain
the same version-stamped static binary as the native release, CA certificates,
timezone data, and zstd for automatic backups. Imvault also includes ffmpeg and ffprobe for clip posters and duration checks.

## Start an instance

Download `docker-compose.yml` from the matching release's source archive or
repository tag, then run from its directory:

```sh
docker compose pull
docker compose up -d
docker compose exec imvault create-admin --username owner --password-prompt
```

Open `http://localhost:8080`. The example publishes only on host loopback.
For access from another computer, configure a reverse proxy and set
`IMVAULT_BASE_URL` to your public HTTPS origin. HTTPS base URLs enable secure
cookies. Trust only the proxy's actual container or host network address in
`IMVAULT_TRUSTED_PROXIES`; loopback inside a container refers to that container.
Each app runs independently; companion app URLs are optional.

The process runs as UID/GID `10001:10001`, with a read-only root filesystem,
no Linux capabilities, and bounded writable `/tmp`. The named data volume is
private, mode `0700`, and survives container replacement. Bind mounts require
you to create the directory with ownership `10001:10001` and mode `0700` first.
Do not use `docker compose down -v` unless you intend to delete the data.
The image healthcheck probes `/healthz` using the port in `IMVAULT_ADDR`.
Override the probe if you bind to a specific non-loopback address.

Set `IMVAULT_IMAGE` in `.env` to choose an explicit version or digest:

```dotenv
IMVAULT_IMAGE=ghcr.io/airencracken/imvault:v0.20.1
```

For a digest pin, use the registry's full multi-platform index digest, such as
`ghcr.io/airencracken/imvault@sha256:...`, obtained with
`docker buildx imagetools inspect ghcr.io/airencracken/imvault:v0.20.1`.
Exact version tags are preserved. We publish no moving `latest`, major, or minor
aliases, so pulling an image never opts you into a later database migration.

## Back up, upgrade, and restore

Automatic compressed backups live under `/data/backups` with bounded retention;
see [automatic backups](automatic-backups.md). Copy backups off the Docker host.
The following full-volume backup also preserves keys, uploaded files, and
scheduling state. Stop the server before taking it:

```sh
umask 077
docker compose stop imvault
docker compose run --rm --no-deps -T --entrypoint tar imvault -C /data -cf - . > imvault-before-upgrade.tar
```

Check that the backup command succeeded and keep the file private. Read the new
release's migration notes, change `IMVAULT_IMAGE` to the desired exact version,
then run `docker compose pull` and `docker compose up -d`. Check
`docker compose ps` and `docker compose logs imvault` before resuming use.

A downgrade requires the matching pre-upgrade data backup as well as the old
image. Startup migrations are forward-only. To restore without destroying the
current volume, stop the service and select a **new** named volume in a Compose
override file:

```yaml
# docker-compose.restore.yml
volumes:
  imvault-data:
    name: imvault-restored
```

Choose the image version matching the backup in `.env`. From the same project
directory, run:

```sh
docker compose -f docker-compose.yml -f docker-compose.restore.yml run --rm --no-deps -T --entrypoint tar imvault -C /data -xf - < imvault-before-upgrade.tar
docker compose -f docker-compose.yml -f docker-compose.restore.yml up -d
```

The destination volume must be new and empty. Keep using that override for
subsequent Compose commands. Verify health, sign-in, and your data before deleting
the original volume. Treat backup files as trusted private inputs.

## Build locally

A source checkout supports an explicit developer build:

```sh
VERSION=0.20.1 docker compose -f docker-compose.yml -f docker-compose.build.yml up --build -d
```

The override builds `imvault:local`; it does not fetch the published app image.
Published images use `Dockerfile.release`, which packages verified native release
artifacts. It does not compile source again.

## Release maintenance

The release workflow builds native archives first. Both amd64 and arm64 images
are tested on native runners for startup, routes, health checks, non-root volume
permissions, graceful shutdown, compressed backups, restart persistence, a
previous-release upgrade, full-volume restore, rollback with the old data, and
adversarial configuration. Unit and mutation tests cover archive checksums,
unsafe archives, metadata, and interrupted publication. Only after these checks
pass does the workflow publish the exact saved images and assemble one combined
version tag. PR and branch builds never publish.

Publication uses the repository's `GITHUB_TOKEN` with `packages: write` only in
the publisher. On the first release, GHCR initially creates a private package:
open the account's **Packages → imvault → Package settings → Change visibility**
and make it public. This is separate from repository visibility. The publisher
verifies access with an empty Docker credential directory and fails until
anonymous access works. Retry that failed publisher job after changing visibility;
its saved artifacts are retained for seven days. Re-running the same artifacts is
idempotent; a different image cannot overwrite an existing version tag.

Update the pinned multi-platform Alpine and Go base digests through reviewed
changes. Refresh runtime packages and run the lifecycle tests for each release.
Security fixes to the runtime need a new patch version; do not replace an old
tag. Candidate architecture tags support publication and are not deployment
references. Keep the combined release tag and digest available for existing users.

Local checks:

```sh
make test-containers
docker build --build-arg VERSION=0.20.1 -t imvault:local .
make test-container-lifecycle VERSION=0.20.1
```
