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
sudo make install-openrc

# Alpine
sudo adduser -S -D -H -h /var/lib/imvault -s /sbin/nologin imvault

# Gentoo
sudo useradd --system --home-dir /var/lib/imvault -s /sbin/nologin imvault

sudo vi /etc/conf.d/imvault           # optional; every setting has a default
sudo rc-update add imvault default
sudo rc-service imvault start
```

Settings in `/etc/conf.d/imvault` use the same `IMVAULT_*` names the server
reads from its environment. The init script re-reads that file with `allexport`
set, so any `IMVAULT_*` value written there reaches the daemon without the
script having to know the option's name. Logs go to `/var/log/imvault.log`.

## Packages

`contrib/` also carries the beginnings of distribution packaging:

| Path | What it is |
| --- | --- |
| `contrib/systemd/` | The unit and its environment file |
| `contrib/openrc/` | The OpenRC script and its configuration file |
| `contrib/alpine/APKBUILD` | An Alpine package recipe, with a `pre-install` that creates the account |
| `contrib/gentoo/imvault-0.1.0.ebuild` | A Gentoo ebuild with `EGO_SUM` filled in from `go.sum` |

The Alpine package has been built, installed, and run on Alpine 3.24: `abuild`
produces `imvault-0.1.0-r0.apk`, the pre-install creates the service account,
and the packaged binary serves. Note that the Go dependencies need **Go 1.26**,
so it builds on Alpine **3.24 or later**; older releases stop with "module
requires go >= 1.26". The APKBUILD needs `abuild checksum` run once against the
release tarball to fill in the checksum.

**The Gentoo ebuild has not been built on Gentoo.** Its `EGO_SUM` matches
`go.sum` line for line and it parses as shell, but the eclass usage is
unverified: treat it as a starting point. The OpenRC script it installs is the
same one Alpine uses and is tested.

## Verifying a deployment

```bash
systemctl status imvault          # or: rc-service imvault status
curl -fsS localhost:8080/healthz  # "ok"
journalctl -u imvault -n 20       # or: tail /var/log/imvault.log
```

The service runs as an unprivileged account, keeps everything but its data
directory read-only, and shares no temporary space with the rest of the system.

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
