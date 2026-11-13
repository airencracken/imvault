# Linux confinement

Bubblewrap confinement is optional. Once you turn it on, a missing tool, a
blocked namespace or a bad mount stops startup with an error. Imvault never
falls back to running unconfined.

Install your distribution's patched `bubblewrap` package and allow unprivileged
user namespaces for the service account. The launcher refuses to run as root
and rejects a setuid Bubblewrap. If kernel or AppArmor policy blocks startup,
`imvault sandbox --check` reports the error without starting the server. Fix
the policy; do not loosen host security to hide the error.

## OpenRC

Add this to `/etc/conf.d/imvault` and restart the service:

```sh
IMVAULT_SANDBOX="true"
```

The service keeps its user, data directory and log file. On shutdown the
launcher forwards SIGTERM and waits twenty seconds.

To test the policy first, run the check as the service user (not root) with
the service's settings loaded. The data directory must already exist.

```sh
IMVAULT_DATA_DIR=/var/lib/imvault imvault sandbox --check
```

## systemd

The shipped unit already confines filesystem access. To add Bubblewrap, create
a drop-in with `systemctl edit imvault`:

```ini
[Service]
ExecStart=
ExecStart=/usr/bin/imvault sandbox
RestrictNamespaces=user mnt pid ipc uts net
ProtectKernelTunables=no
ReadOnlyPaths=/sys
RestrictSUIDSGID=no
KillMode=mixed
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
```

Use `/usr/local/bin/imvault` for a source install. Keep the unit's other
hardening settings. What each override does:

- `RestrictNamespaces` permits only the namespaces Bubblewrap needs.
- `ProtectKernelTunables=no` lets Bubblewrap mount its own procfs;
  `ReadOnlyPaths=/sys` keeps host sysfs read-only.
- `RestrictSUIDSGID=no` allows syscalls newer Bubblewrap versions use.
  `NoNewPrivileges=yes` still blocks setuid gains.
- `KillMode=mixed` lets the launcher stop the server before systemd kills
  what remains.
- `AF_NETLINK` lets Bubblewrap bring up loopback in each media job's isolated
  network namespace.

Run `systemctl daemon-reload` and restart. To turn confinement off, remove the
drop-in.

## What the server can access

Inside the sandbox the server sees, read-only: `/usr`, binary and library
directories, the linker cache, CA certificates, DNS and hosts configuration,
and timezone data. If `SSL_CERT_FILE` is set, that bundle must be a regular
file and replaces the system one. The server keeps the host network. It gets
its own process namespace, minimal devices and a private `/tmp`. Only the data
directory is writable. Other home directories, `/etc/shadow`, host temporary
files and host Unix sockets are absent. `/usr` stays visible, so keep
credentials out of it.

The sandbox passes through Imvault's settings, `TZ` and the AWS SDK variables
(`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`,
`AWS_REGION`, `AWS_PROFILE`, `AWS_CONFIG_FILE`, `AWS_SHARED_CREDENTIALS_FILE`,
`AWS_CA_BUNDLE`, the `AWS_ENDPOINT_URL` pair, and the web-identity and
container-credential variables). It drops every other variable. Any file those
variables name, such as a shared credentials file or CA bundle, must be added
with `--read-file`, because `HOME` inside the sandbox is the private `/tmp`.

Unless `IMVAULT_MEDIA_SANDBOX=true`, user namespaces are disabled inside the
sandbox. The server runs as the namespace's first process and reaps orphaned
children, such as a media job killed by a timeout.

### Extra paths

- `--write-dir /srv/imvault-objects` adds an existing writable directory, for
  storage or a database kept outside the data directory.
- `--read-file /path/to/file` adds a read-only file, such as a certificate or key.
- `--bwrap /path/to/bwrap` picks the Bubblewrap executable. `IMVAULT_BWRAP`
  sets the default.

Both path flags can repeat. OpenRC takes one of each through
`IMVAULT_SANDBOX_WRITE_DIR` and `IMVAULT_SANDBOX_READ_FILE`, as literal paths.
The launcher refuses broad mounts such as `/`, `/var`, `/home` and `/tmp`,
writable mounts over system directories, and symlinks to either.

### Limits

A compromised server can still read its own data and use the network. The
sandbox protects the rest of the host, not the database. It sets no CPU, memory
or disk quota; use your service manager for those.

The sandbox's `/tmp` is memory. Uploads past 8 MiB, S3 upload spools and clip
scratch copies land there. Size `MemoryMax=` (systemd) or the container's memory
for `IMVAULT_MAX_CONCURRENT_UPLOADS` × `IMVAULT_MAX_VIDEO_BYTES`.

## Media subprocesses

To confine FFmpeg and FFprobe separately, set this in the service environment,
or export it for maintenance commands such as `rebuild-thumbnails`:

```sh
IMVAULT_MEDIA_SANDBOX="true"
```

It needs both FFmpeg tools and working Bubblewrap. The server checks at
startup. If confinement fails later, the operation fails; media is never
processed unconfined. Still images are always decoded inside the server.

Each media job can read one input file and the system binaries and libraries.
It can write only to private scratch space and a fresh output directory. It has
no network, environment, database, encryption key, storage credentials or other
originals. Probing, posters and metadata removal all run this way. Output must
be regular files; symlinks are rejected.

With the shipped systemd unit, apply the `RestrictNamespaces`,
`ProtectKernelTunables`, `ReadOnlyPaths`, `RestrictSUIDSGID` and
`RestrictAddressFamilies` overrides above even if `ExecStart` stays unchanged.
Whole-service and media confinement can run together.
