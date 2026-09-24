# Binary releases

Download imvault from [GitHub Releases](https://github.com/airencracken/imvault/releases).
Each version has Linux **amd64** (x86-64) and **arm64** (AArch64) builds:

- `imvault_VERSION_linux_ARCH.tar.gz`: a static binary, documentation,
  license notices, and service/configuration examples.
- `imvault_VERSION_ARCH.deb`: a Debian/Ubuntu package with a systemd unit.
- `imvault_VERSION_source.tar.gz`: the corresponding source.
- `imvault_VERSION_checksums.txt`: SHA-256 checksums for all five artifacts.

The Go compiler is only needed when building from source.
Install ffmpeg for video thumbnails and duration checks; the Debian package
recommends it. HTTPS connections also need your system's CA certificates.

## Debian and Ubuntu

Select the version and architecture you want. For example:

```sh
release_version=0.5.0
release_arch=amd64
release_url="https://github.com/airencracken/imvault/releases/download/v$release_version"
curl -fLO "$release_url/imvault_${release_version}_${release_arch}.deb"
curl -fLO "$release_url/imvault_${release_version}_checksums.txt"
sha256sum --ignore-missing -c "imvault_${release_version}_checksums.txt"
sudo apt install "./imvault_${release_version}_${release_arch}.deb"
```

Use `arm64` for an AArch64 machine. Check that the checksum command succeeds
before installing. The package creates a dedicated `imvault` account and
`/var/lib/imvault` with mode 0700. It installs the binary in `/usr/bin` and
keeps settings in `/etc/imvault/imvault.env`, readable only by root.

**The first install leaves the service stopped and disabled.** Edit that
configuration, provision the administrator, then enable the service. Native package
defaults bind to `127.0.0.1:8080`; configure your HTTPS reverse proxy and
secure cookies before exposing the site.

In a terminal, provision the account using the same data directory. The hidden
prompt asks for the password twice:

```bash
sudo /usr/bin/imvault create-admin \
	--username alex --password-prompt
systemctl enable --now imvault
```

The command reads `IMVAULT_DATA_DIR` and `IMVAULT_DB` from the systemd unit and
environment file or OpenRC configuration when they are unset in the process
environment. When run as root, it repeats the database operation as the
configured service account. For scripts, pass one password line on stdin with
`--password-stdin`. See
[deployment](deployment.md) for reverse proxy and service settings. Logs go to
`journalctl -u imvault`.

An upgrade restarts a running service and leaves an inactive service inactive.
Back up before upgrading: startup can migrate the database. Debian manages
the environment file as a conffile, preserving local edits or asking how to
handle a changed upstream default.

Removal stops and disables the service. Purge also removes package
configuration. **Neither removes application data or the service account.**
Delete those separately only when you intend to discard the installation.
An older binary may require restoring the matching pre-upgrade backup.

## Portable binary archive

Download the archive and checksum file for your architecture, verify them as
above, and extract the archive:

```sh
tar -xzf imvault_0.5.0_linux_amd64.tar.gz
cd imvault_0.5.0_linux_amd64
sudo install -m 0755 imvault /usr/local/bin/imvault
sudo install -d /usr/local/share/doc/imvault
sudo install -m 0644 LICENSE README.md THIRD_PARTY_NOTICES.txt /usr/local/share/doc/imvault/
```

For an existing service, install the binary at the path its service definition
uses, then restart it. For a new installation, create its dedicated service
account and data directory as described in [deployment](deployment.md).

To install the systemd files directly from the archive on a new installation:

```sh
sudo install -m 0644 contrib/systemd/imvault.service /etc/systemd/system/imvault.service
sudo install -d /etc/imvault
sudo install -m 0600 contrib/systemd/imvault.env /etc/imvault/imvault.env
sudo systemctl daemon-reload
```

Edit the environment file (including `IMVAULT_ADDR=127.0.0.1:8080`),
provision the local account, and enable the service. These first-install
commands copy configuration; keep your existing configuration during upgrades.
For OpenRC, the archive includes `contrib/openrc` and `contrib/logrotate`.
Set `IMVAULT_BIN` to the installed binary path in `/etc/conf.d/imvault`.

## Logging in packages

Gentoo's ebuilds install `/etc/logrotate.d/imvault` and depend on
`app-admin/logrotate`. The OpenRC service writes `/var/log/imvault.log`;
the rule keeps 14 archives and rotates daily or above 10 MiB when the system's
logrotate job runs. Gentoo's default logrotate installation provides its cron
job and depends on a cron implementation. If you disable that integration,
arrange the system logrotate timer or another scheduler.

Debian packages and the supplied systemd unit explicitly send stdout and stderr
to journald, which handles rotation and retention. They do not create a separate
application log file. Use `journalctl -u imvault` to read these logs.
Journal limits are managed by the host's journald configuration.

## Building and publishing

GoReleaser **2.18.2** builds the same artifact set for both projects. With Go
and GoReleaser installed:

```sh
make release-check
make release-snapshot
sh scripts/release/check-artifacts.sh
```

Snapshot builds stay in `dist/` and never publish. Artifact checks also need
`dpkg-deb`, `readelf`, and standard shell tools; Debian/Ubuntu provide these in
`dpkg` and `binutils`. Source archives come from Git, so commit changes before
checking that an archive matches the working tree. Generated dependency notices
and package files live in the ignored `.release/` directory.

The GitHub Actions workflow runs repository checks, builds both architectures,
validates archive/package contents and checksums, and tests package lifecycles
on native amd64 and arm64 Ubuntu runners plus Debian containers. It checks
owner provisioning, HTTP health, service startup and upgrade restart on systemd,
conffile preservation, removal, purge, and reinstallation with existing data.
The lifecycle script intentionally refuses an existing installation and requires
`RELEASE_PACKAGE_TEST=1` in a disposable system.

After the change is on `master`, publish by creating and pushing a version tag:

```sh
git tag -a vX.Y.Z -m "imvault X.Y.Z"
git push origin vX.Y.Z
```

The tagged commit must be reachable from `master`. The workflow publishes
the exact tested artifacts to GitHub Releases only after every check passes.
Uploads happen in a draft before it becomes public. Tags with a prerelease
suffix produce a prerelease. A rerun can finish a draft but will not replace
an already published release.

The workflow uses GitHub's repository token with write access confined to
the publishing job; no separate release token is needed.
