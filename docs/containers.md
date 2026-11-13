# Run Imvault with Docker

Images for Linux amd64 and arm64 are published as
`ghcr.io/airencracken/imvault:vX.Y.Z`; Docker picks the right architecture.
Each image holds the same static binary as the native release, plus CA
certificates, timezone data, zstd for automatic backups, and ffmpeg and ffprobe
for clip posters and duration checks.

## Start an instance

Download `docker-compose.yml` from the release's source archive or repository
tag. In its directory, run:

```sh
docker compose pull
docker compose up -d
docker compose exec imvault create-admin --username owner --password-prompt
```

Open `http://localhost:8080`. The example listens only on host loopback. To
serve other computers, put a reverse proxy in front and set `IMVAULT_BASE_URL`
to your public HTTPS origin, which also turns on secure cookies. List only the
proxy's real container or host address in `IMVAULT_TRUSTED_PROXIES`; loopback
inside a container means that container.

The server runs as UID and GID `10001` with a read-only root filesystem, no
Linux capabilities, and a 512 MiB in-memory `/tmp`. The named data volume gets
the right owner and mode `0700` automatically and survives container
replacement. For a bind mount, create the directory first, then
`chown 10001:10001` and `chmod 0700` it. `docker compose down -v` deletes the
data volume.

The image's healthcheck probes `/healthz` on the port in `IMVAULT_ADDR`, so
`docker ps` shows when the server and database answer. Override the probe if
you bind to a specific non-loopback address.

Pin a version in `.env`:

```dotenv
IMVAULT_IMAGE="ghcr.io/airencracken/imvault:v0.26.1"
```

To pin a digest, use the full multi-platform index digest
(`ghcr.io/airencracken/imvault@sha256:...`) from
`docker buildx imagetools inspect ghcr.io/airencracken/imvault:v0.26.1`.
There are no `latest`, major or minor tags, so a pull never brings in a
database migration you did not choose.

## Back up, upgrade, and restore

Automatic backups land in `/data/backups`; see
[automatic backups](automatic-backups.md) and copy them off the Docker host. A
full-volume backup also keeps keys, uploads and scheduling state. Stop the
server first:

```sh
umask 077
docker compose stop imvault
docker compose run --rm --no-deps -T --entrypoint tar imvault -C /data -cf - . > imvault-before-upgrade.tar
```

Check that the command succeeded and keep the file private. To upgrade, read
the new release notes, set `IMVAULT_IMAGE` to the new version, and run
`docker compose pull` and `docker compose up -d`. Check `docker compose ps` and
`docker compose logs imvault` before you carry on.

Migrations only go forward, so a downgrade needs the old image and the matching
pre-upgrade backup. Restore into a **new**, empty volume so the current one
stays intact. Stop the service and add a Compose override:

```yaml
# docker-compose.restore.yml
volumes:
  imvault-data:
    name: imvault-restored
```

Set `.env` to the image version that matches the backup, then from the same
directory run:

```sh
docker compose -f docker-compose.yml -f docker-compose.restore.yml run --rm --no-deps -T --entrypoint tar imvault -C /data -xf - < imvault-before-upgrade.tar
docker compose -f docker-compose.yml -f docker-compose.restore.yml up -d
```

Keep using the override for later Compose commands. Check health, sign-in and
your data before deleting the old volume. Restore only backups you made.

## Build locally

From a source checkout:

```sh
VERSION=0.26.1 docker compose -f docker-compose.yml -f docker-compose.build.yml up --build -d
```

This builds `imvault:local` from source and does not pull the published image.
`make compose-up` runs the same build stamped as `development`;
`make compose-down` stops it.
Published images come from `Dockerfile.release`, which packages the verified
native release binaries.

## Publishing images

The release workflow publishes images only after both architectures pass their
container tests. On the first release, GHCR creates the package as private:
open **Packages → imvault → Package settings → Change visibility**, make it
public, and re-run the failed publisher job within seven days. A published
version tag is never overwritten; ship runtime security fixes as a new patch
release.

Local checks:

```sh
make test-containers
docker build --build-arg VERSION=0.26.1 -t imvault:local .
make test-container-lifecycle VERSION=0.26.1
```
