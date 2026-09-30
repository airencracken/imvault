# Linux confinement

Bubblewrap support is optional. Normal startup is unchanged. Once you request
confinement, missing tools, blocked namespaces, or invalid mounts cause an error;
the application never retries outside the sandbox.

Install your distribution's current, patched `bubblewrap` package. Unprivileged
user namespaces must be permitted for the service account. The launcher refuses
to run as root and rejects setuid Bubblewrap installations. Kernel or AppArmor policy may prevent startup; `sandbox --check`
reports the error without starting the server. Do not weaken host policy merely
to hide an error.

## OpenRC

Add this to `/etc/conf.d/imvault`, then restart the service:

```sh
IMVAULT_SANDBOX=true
```

The service continues to use its existing user, data directory and log file.
Logs remain on stdout/stderr, with OpenRC handling redirection outside the
sandbox. The launcher forwards SIGTERM and allows twenty seconds for shutdown.

You can verify the policy first, as the service account with its application
settings loaded:

```sh
IMVAULT_DATA_DIR=/var/lib/imvault imvault sandbox --check
```

Run that command using the configured service user, not root. The data directory
must already exist. Provision accounts using the existing account commands.

## systemd

The shipped unit already confines filesystem access. To also use Bubblewrap,
create a drop-in with `systemctl edit imvault`:

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

Use `/usr/local/bin/imvault` for a default source installation. Keep the unit's
other hardening settings. Its default `RestrictNamespaces=yes` blocks the
namespaces Bubblewrap needs; the override permits only the listed types.
`ProtectKernelTunables=yes` masks parts of procfs, which prevents an unprivileged
Bubblewrap process from mounting its own procfs. The override removes that
conflict while keeping host sysfs read-only. Bubblewrap supplies its own private
procfs with protected kernel-control paths for the confined server and tools.
`RestrictSUIDSGID=no` permits the path-resolution syscalls used by newer
Bubblewrap versions; `NoNewPrivileges=yes` continues to block setuid privilege
gains. `KillMode=mixed` sends SIGTERM to the launcher first, so it can let the
server shut down before systemd kills any remaining processes.
`AF_NETLINK` lets Bubblewrap initialize loopback inside each media job's isolated
network namespace; the job still cannot reach the host network.
Run `systemctl daemon-reload`, then restart the service. To disable whole-service
Bubblewrap, remove these overrides and restore the packaged unit's restrictions.

## What the server can access

The sandbox has read-only `/usr`, binary and library directories, the dynamic linker cache and library alternatives, system CA
certificates, DNS/hosts configuration and local timezone information. It retains
the host network for HTTP, mail and other configured services. It has its own
process namespace, minimal devices, and temporary directory. Only its configured
data directory is writable on the host by default. Other home directories,
`/etc/shadow`, host temporary files and host Unix sockets are absent.

Application settings are passed in the environment, including credentials the
server needs. Unrelated environment variables and dynamic-loader settings are
removed. Credentials are never put in Bubblewrap's command-line arguments.
`/usr` remains visible: do not place private application credentials there.

For a custom storage/database directory outside the data directory, explicitly
add an existing writable mount with `sandbox --write-dir /srv/imvault-objects`.
For a custom certificate or key file, use `--read-file /path/to/file`. Both flags
can be repeated. OpenRC supports one extra directory and file through
`IMVAULT_SANDBOX_WRITE_DIR` and `IMVAULT_SANDBOX_READ_FILE`; these are literal
paths, not shell arguments. Broad mounts such as `/`, `/var`, `/home`, and `/tmp`,
and writable mounts over runtime/system directories, are refused, including
symlinks pointing to them. `--bwrap /path/to/bwrap` selects a custom executable;
`IMVAULT_BWRAP` supplies its default.

A compromised server can still access its own data and use the network. This
limits access to the rest of the host; it does not protect the database from the
application itself. Bubblewrap is not a CPU, memory or disk quota. Existing
request limits and timeouts still apply; use your service manager for resource
limits.

## Verification

`make test-sandbox` requires real Bubblewrap namespaces. It tests hidden host
files, environment filtering, writable data, retained server networking and
graceful shutdown. Setup failures fail the tests rather than skipping them.
`make test-sandbox-mutations` checks deliberate policy regressions.

## Media subprocesses

Enable the stricter FFmpeg/FFprobe sandbox independently:

```sh
IMVAULT_MEDIA_SANDBOX=true
```

Set it in the service's environment file, or export it for maintenance commands
such as `rebuild-thumbnails`. Both FFmpeg tools and working Bubblewrap are
required when this setting is enabled. The server checks namespace availability
at startup; a later setup failure rejects the operation without unrestricted
processing. Still images continue to be decoded inside the Go server.

Each media invocation can read one input file and the runtime binaries/libraries.
It can write only to private temporary space and a fresh output directory. It
has no network, application environment, database, encryption key, object-store
credentials, or access to other originals. Nested user namespaces are disabled.
Probing, poster generation and metadata removal all use this boundary. Outputs
must be regular files; symlinks are rejected before copying back to the server.

When enabling this with the shipped systemd unit, also override
the same `RestrictNamespaces`, `ProtectKernelTunables`, `ReadOnlyPaths` and
`RestrictSUIDSGID` and `RestrictAddressFamilies` settings shown above, even if you leave `ExecStart` unchanged. Whole-service and media confinement can be enabled together.
