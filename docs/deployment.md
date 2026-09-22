# Deployment

Running imvault on a server, without Docker.

A release is a single static binary, a data directory, and an optional
configuration file. Nothing else is needed.

```bash
make dist                      # dist/imvault-<version>.tar.gz
```

Or build in place and install:

```bash
make build
sudo make install              # /usr/local/bin/imvault
```

Create the account it runs as, then pick one of the two init systems below.

```bash
sudo useradd --system --home-dir /var/lib/imvault --shell /usr/sbin/nologin imvault
```

## systemd

```bash
sudo make install-systemd
sudo vi /etc/imvault/imvault.env      # optional; every setting has a default
sudo systemctl daemon-reload
sudo systemctl enable --now imvault
journalctl -u imvault -f
```

The unit keeps everything but `/var/lib/imvault` read-only, drops privileges,
and gives the service its own `/tmp` for the temporary copies ffmpeg makes.
`StateDirectory=imvault` creates the data directory with the right ownership on
first start.

**If you move the data directory**, add it to `ReadWritePaths` in the unit or
`ProtectSystem=strict` will make it read-only and the service will not start.

The hardening is deliberately the safe subset. A system call filter
(`SystemCallFilter=@system-service`) is a reasonable addition, but imvault shells
out to ffmpeg for clip posters and a filter that turns out to be too tight would
break video silently, so it is left for you to add once you have checked the
journal after an upload.

## OpenRC

Both are covered by the same init script; they differ only in how the service
account is created.

```bash
# Install the binary where the service looks for it.
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

`PREFIX` defaults to `/usr/local`, but the init script looks for the binary at
`/usr/bin/imvault`, which is where a package installs it. Either pass
`PREFIX=/usr` as above, or set `IMVAULT_BIN` in `/etc/conf.d/imvault`;
`make install-openrc` says so when the two disagree, because the alternative is
a service that starts and immediately stops.

Settings in `/etc/conf.d/imvault` use the same `IMVAULT_*` names the server
reads from its environment. The init script re-reads that file with `allexport`
set, so any `IMVAULT_*` value written there reaches the daemon without the
script having to know the option's name. Logs go to `/var/log/imvault.log`.

Two things that are easy to miss on a host rather than in a container:

- **ffmpeg is optional but not free.** Without it clips still upload, but they
  get a placeholder poster and the duration limit cannot be enforced, so set
  `IMVAULT_MAX_VIDEO_DURATION` down or accept unmeasured clips.
- **Temporary space.** Multipart bodies spill past 8 MiB into `TMPDIR`, and one
  request may carry twenty files. The container gets a sized tmpfs for that; on
  a host it is `/tmp`, so either give it room or point `TMPDIR` at a filesystem
  with some. See [Operations](operations.md#the-temporary-directory).

## TLS with Caddy

[Caddy](https://caddyserver.com) obtains and renews a certificate on its own, so
the only things needed are a hostname pointed at the machine and that hostname
in the site address. There is no certificate command to run and nothing to
renew by hand.

```bash
sudo cp contrib/caddy/Caddyfile /etc/caddy/Caddyfile
sudo editor /etc/caddy/Caddyfile      # put your hostname in the site address
sudo systemctl reload caddy
```

For a machine on a trusted network, or for trying the proxy out before pointing
a domain at it, `contrib/caddy/Caddyfile.local` does the same over plain HTTP on
port 8081.

`contrib/caddy/docker-compose.yml` runs Caddy and imvault together, with imvault
publishing no ports of its own: an application port left open beside a TLS
terminator is a way around it.

### The three things that matter

1. **Bind imvault to loopback.** `IMVAULT_ADDR=127.0.0.1:8080` means the only
   way in is through the proxy. Publishing 8080 as well would let anyone reach
   the plaintext origin and its non-Secure cookies.

2. **Set `IMVAULT_TRUST_PROXY_HEADERS=true`.** Without it every request appears
   to come from the proxy, so anonymous uploads and sign-in attempts all share
   one rate-limit bucket.

3. **You probably do not need `IMVAULT_SECURE_COOKIES`.** Caddy sets
   `X-Forwarded-Proto`, and imvault marks cookies `Secure` when it sees `https`.
   Set it anyway if you would rather not depend on the header.

### What was checked

The example config was run against a real instance, and the following were
observed rather than assumed:

- **Caddy discards a client-supplied `X-Forwarded-For`** and replaces it with
  the peer it accepted the connection from, so the header imvault trusts is the
  real one. The rate limiter was confirmed to key on the forwarded address
  (`key=login:198.51.100.9:marcus`) when the proxy reported it.
- **Range requests survive the proxy**, which matters for scrubbing a clip.
- **Compression is applied by content type**: a JPEG came back with no
  `Content-Encoding` while the stylesheet was gzipped, so images and zip exports
  are not paying for a pointless deflate.
- **A streaming export passes through intact** — the zip arrived complete, with
  no CRC errors.

### One thing to be careful of

**Do not add a strict `Content-Security-Policy` without reading
[Security](security.md) first.** imvault's admin pages use inline Alpine
expressions, which are evaluated with `new Function`, so a policy without
`unsafe-eval` will break the confirmation dialog and the table filter — silently,
as a console error rather than a visible failure.

If Caddy is itself behind another proxy or a CDN, add that proxy to Caddy's
`trusted_proxies` so `X-Forwarded-For` reflects the original client rather than
the last hop.

## Packages

`contrib/` also carries the beginnings of distribution packaging:

| Path | What it is |
| --- | --- |
| `contrib/systemd/` | The unit and its environment file |
| `contrib/openrc/` | The OpenRC script and its configuration file |
| `contrib/alpine/APKBUILD` | An Alpine package recipe, with a `pre-install` that creates the account |
| `contrib/gentoo/imvault-9999.ebuild` | A live Gentoo ebuild that builds master, for an overlay |

The Alpine package has been built, installed, and run on Alpine 3.24: `abuild`
produces `imvault-0.1.0-r0.apk`, the pre-install creates the service account,
and the packaged binary serves.

Building from source needs **Go 1.26**, which is not the language level of the
code but the declared requirement of the pinned dependencies: `x/crypto`,
`x/sys`, `x/image`, and `x/oauth2` all ask for 1.26.0 in their own `go.mod`, and
a toolchain below that refuses before it compiles anything. A stable Alpine
branch keeps the Go major it shipped with rather than tracking new ones, so the
floor is fixed at release time:

| Alpine | Released | Go | `abuild` with the packaged toolchain |
| --- | --- | --- | --- |
| 3.24 | June 2026 | 1.26.8 | yes |
| 3.23 | December 2025 | 1.25.10 | no, "module requires go >= 1.26" |

That is a choice of dependency versions rather than a requirement of the program.
Holding the four modules back to their 1.25-era releases would lower the floor
and bring 3.23 into range, at the cost of pinning the security-critical ones to
older code. The trade taken here is to track them.

Only a native build is affected. The container compiles with the upstream
`golang` image, and **running needs no Go at all**, so any Alpine runs the
static binary: the supplied Dockerfile runs it on 3.20.

The APKBUILD needs `abuild checksum` run once against the release tarball to fill
in the checksum.

**The Gentoo ebuild is a live one.** It builds
the `master` branch through `git-r3` and vendors the modules with
`go-module_live_vendor`, so there is no version to bump, no manifest to
regenerate, and no `EGO_SUM` to keep in step with `go.sum` — which the eclass now
deprecates in favour of a dependency tarball in any case. The `LICENSE` line
covers imvault and every module linked into the binary, worked out from
`go list -deps` rather than from the module graph, so it does not list the
test-only dependencies that never end up in it.

Copy all three packages into an overlay already configured in Portage:

```bash
sudo install -d /var/db/repos/local/app-admin/imvault
sudo install -m644 contrib/gentoo/imvault-9999.ebuild contrib/gentoo/metadata.xml \
  /var/db/repos/local/app-admin/imvault/
sudo cp -R contrib/gentoo/acct-user contrib/gentoo/acct-group /var/db/repos/local/
sudo emerge -av app-admin/imvault
```

The account packages use Gentoo's `acct-user` and `acct-group` eclasses with
dynamically allocated IDs for a private overlay. The package installs both init
scripts and the binary at `/usr/bin/imvault`; OpenRC exports the default
`/var/lib/imvault` data directory even when the config file contains only comments.
Enable the `ffmpeg` USE flag for clip posters and duration checks.

The recipes pass `pkgcheck` against the local Gentoo tree. The Go build and
OpenRC environment checks run locally, but a full `emerge` installation and
service start on mordor remain deployment checks.

A versioned ebuild needs a release source tarball, a dependency tarball, and a
manifest. The live ebuild vendors modules during unpack instead.

Before exposing the service, bind it to loopback, configure the TLS proxy, and
register your administrator account. Under **Admin → Settings → Anonymous
uploads**, uncheck **Allow uploads without an account** and save to turn them
off. The switch applies immediately and survives restarts. Set an instance
storage ceiling and upload concurrency appropriate to the machine.

## Verifying a deployment

```bash
systemctl status imvault          # or: rc-service imvault status
curl -fsS localhost:8080/healthz  # "ok"
journalctl -u imvault -n 20       # or: tail /var/log/imvault.log
```

The service runs as an unprivileged account, keeps everything but its data
directory read-only, and shares no temporary space with the rest of the system.

## Temporary space

The server spills multipart uploads past 8 MiB into `TMPDIR`. The container sets
it to `/tmp` and both compose files mount a 512 MiB tmpfs there, so a runaway
spill fails an upload rather than filling the filesystem the database is on. See
[Operations](operations.md#the-temporary-directory) for the arithmetic and how
to size it.

## Logs

Every line is logfmt — a flat sequence of `key=value` pairs, one record per
line — so logs can be read by eye and parsed without a schema:

```
time=2026-09-18T11:21:14.937-07:00 level=INFO msg="imvault listening" addr=:8080 data_dir=/var/lib/imvault
time=2026-09-18T11:21:16.988-07:00 level=WARN msg="mail delivery failed; will retry" id=1 recipient=a@example.com attempt=1 error="smtp: dial tcp 127.0.0.1:9: connect: connection refused"
```

Values containing spaces or newlines are quoted and escaped, so a multi-line
stack trace stays on one line. Set `IMVAULT_LOG_LEVEL` to `debug`, `info`, `warn`
or `error`.
