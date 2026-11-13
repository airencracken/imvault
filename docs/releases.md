# Binary releases

Download Imvault from [GitHub Releases](https://github.com/airencracken/imvault/releases).
The latest release is [0.26.1](release-notes/0.26.1.md). Every version's
changes and upgrade steps are in [release notes](release-notes/); read the
notes for each version you skip.

Each version has Linux **amd64** (x86-64) and **arm64** (AArch64) builds:

- `imvault_VERSION_linux_ARCH.tar.gz`: a static binary, documentation, license
  notices, and service and configuration examples.
- `imvault_VERSION_ARCH.deb`: a Debian and Ubuntu package with a systemd unit.
- `imvault_VERSION_source.tar.gz`: the matching source.
- `imvault_VERSION_checksums.txt`: SHA-256 checksums for all five files.

You need Go only to build from source. Install ffmpeg for video posters and
duration checks; the Debian package recommends it. HTTPS connections need the
system's CA certificates.

## Debian and Ubuntu

Pick a version and architecture, then download, verify and install:

```sh
release_version=0.26.1
release_arch=amd64
release_url="https://github.com/airencracken/imvault/releases/download/v$release_version"
curl -fLO "$release_url/imvault_${release_version}_${release_arch}.deb"
curl -fLO "$release_url/imvault_${release_version}_checksums.txt"
sha256sum --ignore-missing -c "imvault_${release_version}_checksums.txt"
sudo apt install "./imvault_${release_version}_${release_arch}.deb"
```

Use `arm64` on an AArch64 machine. Install only if the checksum command
succeeds.

The package creates an `imvault` account and `/var/lib/imvault` with mode 0700.
It installs the binary as `/usr/bin/imvault` and keeps settings in
`/etc/imvault/imvault.env`, readable only by root.

**The first install leaves the service stopped and disabled.** Edit the
settings file, create the first owner, then start the service. The package
listens on `127.0.0.1:8080`. Set up your HTTPS reverse proxy and a public HTTPS
base URL before you open the site to others; the HTTPS URL turns on secure
cookies.

Create the owner with a hidden password prompt, then start the service:

```bash
sudo /usr/bin/imvault create-admin \
	--username alex --password-prompt
systemctl enable --now imvault
```

The command finds the data directory in the service configuration and runs as
the service account. For scripts, pass one password line on stdin with
`--password-stdin`. [Setup](setup.md) covers the browser alternative, and
[deployment](deployment.md) covers proxy and service settings. Read logs with
`journalctl -u imvault`.

An upgrade restarts a running service and leaves a stopped one stopped. Back up
first: startup can migrate the database, and an older binary refuses a migrated
database. See
[going back to an older binary](operations.md#going-back-to-an-older-binary).
Debian keeps your edits to the settings file and asks before replacing it.

Removing the package stops and disables the service; purging also removes its
configuration. **Neither deletes your data or the service account.** Delete
those yourself only when you mean to discard the installation.

## Portable binary archive

Download the archive and checksum file, verify them as above, and extract:

```sh
tar -xzf imvault_0.26.1_linux_amd64.tar.gz
cd imvault_0.26.1_linux_amd64
sudo install -m 0755 imvault /usr/local/bin/imvault
sudo install -d /usr/local/share/doc/imvault
sudo install -m 0644 LICENSE README.md THIRD_PARTY_NOTICES.txt /usr/local/share/doc/imvault/
```

To upgrade an existing service, install the binary at the path its service
uses and restart it. For a new installation, create the service account and
data directory as described in [deployment](deployment.md), then install the
systemd files from the archive:

```sh
sudo install -m 0644 contrib/systemd/imvault.service /etc/systemd/system/imvault.service
sudo install -d /etc/imvault
sudo install -m 0600 contrib/systemd/imvault.env /etc/imvault/imvault.env
sudo systemctl daemon-reload
```

Edit the settings file (including `IMVAULT_ADDR=127.0.0.1:8080`), create the
owner, and enable the service. Skip these copies on upgrades so your settings
stay put.

For OpenRC, the archive includes `contrib/openrc` and `contrib/logrotate`. Set
`IMVAULT_BIN` to the installed binary in `/etc/conf.d/imvault`. Log rotation is
covered in [operations](operations.md#log-rotation).

## Containers

See [containers](containers.md) for published images, Compose, version pins,
backups, upgrades and restores.

## Building and publishing

For maintainers. With Go installed (Make runs GoReleaser 2.18.2 if you have no
local copy):

```sh
make release-check
make release-snapshot
sh scripts/release/check-artifacts.sh
```

Snapshots stay in `dist/` and never publish. The artifact check also needs
`dpkg-deb` and `readelf` (from `dpkg` and `binutils`). Source archives come
from Git, so commit before comparing an archive with your working tree.

To publish, push a version tag from a commit on `master`:

```sh
git tag -a vX.Y.Z -m "imvault X.Y.Z"
git push origin vX.Y.Z
```

The Release workflow tests both architectures and publishes the tested files
only after every check passes. A tag with a prerelease suffix makes a
prerelease. A rerun can finish a draft but never replaces a published release.

To rebuild an existing tag, dispatch the Release workflow on `master` with
`release_tag` set to that tag. Published releases and image tags do not change.
