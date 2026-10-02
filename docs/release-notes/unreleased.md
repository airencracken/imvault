# Unreleased

Changes since 0.11.1 that operators should know about. Fold this into the next
version's release notes.

## Upgrade notes

- **Everyone is signed out once.** Over HTTPS, the session and CSRF cookies are
  now named `__Host-imvault_session` and `__Host-imvault_csrf`, so a sibling
  subdomain or a plain-HTTP page on the same host can no longer plant them.
  Cookies under the old names are ignored on HTTPS, so existing sessions end at
  the upgrade and people sign in again. Plain-HTTP instances, such as local
  development, keep the old names and their sessions.
- **An older binary will not start against a newer database.** A binary that
  finds migrations in the database it does not have now refuses to start, and
  so does every maintenance command, with a message naming them. Earlier
  versions carried on silently. To go back a version, restore the backup taken
  before the upgrade. See
  [going back to an older binary](../operations.md#going-back-to-an-older-binary).
- **`imvault backup` no longer migrates.** It runs only against a database at
  its own binary's schema, so a backup taken before an upgrade is a copy of the
  database as it was. Take it with the version that last ran the database.
  `imvault restore` refuses a backup taken by a newer Imvault.
- **The data directory is private.** The systemd unit now uses
  `StateDirectoryMode=0700` and `UMask=0077`, and the OpenRC script checks the
  directory at 0700 and starts the server with umask 0077, so an existing
  `/var/lib/imvault` is tightened from 0750 on the next start. The Gentoo and
  Alpine recipes, the container image and the server itself create it at 0700.
  If something else on the host reads the data directory through the
  `imvault` group, such as a backup agent, run it as the service account
  instead.
- **Service stop and start.** systemd's `TimeoutStopSec` and OpenRC's stop
  retry are now 25 seconds, longer than the server's 15-second drain and the
  sandbox launcher's 20-second wait. OpenRC now refuses a relative
  `IMVAULT_BIN` or `IMVAULT_LOG_FILE` as well as `IMVAULT_DATA_DIR`, fails a
  start when the server exits within a second, and no longer resets the mode of
  an existing log directory.
- **Container health.** The image now has a `HEALTHCHECK` that probes
  `/healthz` with the busybox `wget` already in the image, following
  `IMVAULT_ADDR`'s port. The Compose file's own probe is gone in its favour.
  `/data` is created at mode 0700; an existing named volume keeps its mode
  until you change it (`chmod 0700` inside the volume).
