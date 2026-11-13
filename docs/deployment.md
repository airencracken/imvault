# Deployment

This guide covers running Imvault on a host without Docker. For containers, see
[Docker installation and operations](containers.md). For prebuilt binaries and
Debian packages, see [binary releases](releases.md).

## Build and install

Building needs Go 1.26.11 or later. Install ffmpeg for clip posters and
duration checks.

```bash
make build
sudo make install              # /usr/local/bin/imvault
make dist                      # or a source tarball: dist/imvault-<version>.tar.gz
```

Create the service account:

```bash
sudo useradd --system --home-dir /var/lib/imvault --shell /usr/sbin/nologin imvault
```

Then set up systemd or OpenRC below, and create your administrator with
`imvault create-admin` as described in
[provisioning an administrator](accounts.md#provisioning-an-administrator).
`IMVAULT_ALLOW_SIGNUP=false` can stay set throughout; public registration never
grants administrator access.

## systemd

```bash
sudo make install-systemd
sudo vi /etc/imvault/imvault.env      # optional; every setting has a default
sudo systemctl daemon-reload
sudo systemctl enable --now imvault
journalctl -u imvault -f
```

The unit makes everything except `/var/lib/imvault` read-only, drops
privileges and gives the service a private `/tmp`. It creates the data
directory at mode 0700 on first start and runs with `UMask=0077`, because the
directory holds credentials, the encryption key and private uploads. Starting
also tightens a directory an older version created at 0750.

**If you move the data directory**, add it to `ReadWritePaths` in the unit.
Otherwise `ProtectSystem=strict` makes it read-only and the service will not
start.

You can add `SystemCallFilter=@system-service` yourself. Check the journal
after a clip upload, since a filter that is too tight breaks ffmpeg.

## OpenRC

Alpine and Gentoo share one init script. Only account creation differs.

```bash
sudo make install PREFIX=/usr
sudo make install-openrc

# Alpine
sudo adduser -S -D -H -h /var/lib/imvault -s /sbin/nologin imvault
# Gentoo
sudo useradd --system --home-dir /var/lib/imvault -s /sbin/nologin imvault

sudo vi /etc/conf.d/imvault           # optional; every setting has a default
sudo rc-update add imvault default
sudo rc-service imvault start
```

The init script expects `/usr/bin/imvault`, while `PREFIX` defaults to
`/usr/local`. Pass `PREFIX=/usr` as above or set `IMVAULT_BIN` in
`/etc/conf.d/imvault`; `make install-openrc` warns when they disagree.

Keep `/etc/conf.d/imvault` at mode 0600, since it can hold SMTP credentials.
Every `IMVAULT_*` value in it reaches the server. Logs go to
`/var/log/imvault.log`.

On start, the script rejects a relative `IMVAULT_DATA_DIR`, `IMVAULT_BIN` or
`IMVAULT_LOG_FILE`, sets the data directory to mode 0700 and runs the server
with umask 0077. It waits a second to confirm the server stayed up, so a bad
configuration fails the start. Stopping gives in-flight requests 25 seconds.

`make install-openrc` also installs `/etc/logrotate.d/imvault`, keeping an
existing rule. Install logrotate and make sure its cron job or timer runs. To
add only the rule to an existing install, run `sudo make install-logrotate`; no
restart is needed. If you change `IMVAULT_LOG_FILE`, update the rule too. See
[log rotation](operations.md#log-rotation).

For S3 storage settings, see [Storage](storage.md).

## Before you open the doors

- **Bind to loopback and put a TLS proxy in front.** See below.
- **Turn off anonymous uploads** if you do not want them: **Admin → Settings →
  Anonymous uploads**, uncheck **Allow uploads without an account**, and save.
  The change applies immediately and survives restarts.
- **Set a storage ceiling and upload concurrency** that fit the machine.
- **Install ffmpeg.** Without it clips still upload, but
  they get a placeholder poster and the duration limit cannot be checked.
- **Leave room in `/tmp`.** Uploads past 8 MiB spill into `TMPDIR`, and one
  request may carry twenty files. Give `/tmp` space or point `TMPDIR` at a
  filesystem that has it. See
  [the temporary directory](operations.md#the-temporary-directory).

## TLS with Caddy

Caddy is the recommended reverse proxy. For nginx and Apache 2.4, see
[HTTPS reverse proxies](reverse-proxies.md).

Caddy obtains and renews certificates by itself. Point a hostname at the
machine and put it in the site address:

```bash
sudo cp contrib/caddy/Caddyfile /etc/caddy/Caddyfile
sudo editor /etc/caddy/Caddyfile      # put your hostname in the site address
sudo systemctl reload caddy
```

`contrib/caddy/Caddyfile.local` serves plain HTTP on port 8081, for a trusted
network or a trial run before you have a domain.

`contrib/caddy/docker-compose.yml` runs Caddy and Imvault together, with no
Imvault port published. It mounts `contrib/caddy/Caddyfile.compose`, which
reaches Imvault by service name; edit the site address there.

Three settings matter:

1. **`IMVAULT_ADDR=127.0.0.1:8080`.** The proxy becomes the only way in. An
   open 8080 would expose plain HTTP and its cookies.
2. **`IMVAULT_TRUSTED_PROXIES=127.0.0.1/32,::1/128`.** Without it every request
   seems to come from the proxy, and all visitors share one rate-limit bucket.
   Forwarding headers from any other address are ignored. For a proxy on
   another host or container, list its address instead.
3. **`IMVAULT_SECURE_COOKIES` is optional behind Caddy.** Caddy sends
   `X-Forwarded-Proto`, and Imvault marks cookies `Secure` when a trusted
   proxy reports `https`. Set it anyway if you prefer not to rely on the
   header.

If Caddy sits behind another proxy or a CDN, either add that proxy to Caddy's
`trusted_proxies`, or keep Caddy replacing the header and list both proxies in
`IMVAULT_TRUSTED_PROXIES`. Imvault reads `X-Forwarded-For` from the right,
skips trusted proxies, and takes the first address that is not one.

Read [Security](security.md) before adding a strict `Content-Security-Policy`;
some admin pages need it to allow `unsafe-eval`.

## Packages

| Path | What it is |
| --- | --- |
| `contrib/systemd/` | The unit and its environment file |
| `contrib/openrc/` | The OpenRC script and its configuration file |
| `contrib/alpine/APKBUILD` | An Alpine recipe whose `pre-install` creates the account |
| `contrib/gentoo/imvault-9999.ebuild` | A live Gentoo ebuild that builds `master` |

Native Alpine builds need Alpine 3.24 or later, whose Go is new enough; 3.23
ships Go 1.25 and fails. The static binary itself runs on any Alpine. Run
`abuild checksum` once against the release tarball to fill in the APKBUILD
checksum.

The Gentoo ebuild builds `master` through `git-r3` and vendors modules during
unpack, so there is no version to bump. Copy it and its account packages into
an overlay Portage already knows:

```bash
sudo install -d /var/db/repos/local/www-apps/imvault
sudo install -m644 contrib/gentoo/imvault-9999.ebuild contrib/gentoo/metadata.xml \
  /var/db/repos/local/www-apps/imvault/
sudo cp -R contrib/gentoo/acct-user contrib/gentoo/acct-group /var/db/repos/local/
sudo emerge -av www-apps/imvault
```

The package installs the binary at `/usr/bin/imvault` with both init scripts.
Enable the `ffmpeg` USE flag for clip posters and duration checks.

## Check the service

```bash
systemctl status imvault          # or: rc-service imvault status
curl -fsS localhost:8080/healthz  # "ok"
journalctl -u imvault -n 20       # or: tail /var/log/imvault.log
```

Logs are logfmt, one `key=value` record per line, with multi-line values quoted
onto one line:

```
time=2026-09-18T11:21:14.937-07:00 level=INFO msg="imvault listening" addr=:8080 data_dir=/var/lib/imvault
time=2026-09-18T11:21:16.988-07:00 level=WARN msg="mail delivery failed; will retry" id=1 recipient=a@example.com attempt=1 error="smtp: dial tcp 127.0.0.1:9: connect: connection refused"
```

Set `IMVAULT_LOG_LEVEL` to `debug`, `info`, `warn` or `error`.

The running service also makes [automatic backups](automatic-backups.md). For
optional Bubblewrap confinement, see [Linux confinement](sandbox.md).

## Configuration format

Double-quote every value, numbers and booleans included. Service files need no
`export`:

```sh
IMVAULT_BASE_URL="https://imvault.example.org"
IMVAULT_BACKUP_KEEP="7"
```

OpenRC reads `/etc/conf.d/imvault`; systemd reads `/etc/imvault/imvault.env`.
Edit the one for your service manager and restart. Bare and single-quoted values
still work, and upgrades keep your edits. Comfylib's
[shared configuration convention](https://github.com/airencracken/comfylib/blob/master/docs/configuration.md)
covers quoting and escaping.
