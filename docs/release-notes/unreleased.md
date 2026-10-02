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
- **The mail sender is checked at startup.** `IMVAULT_SMTP_FROM` must be a
  single address, optionally with a display name, on one line. A malformed
  value now stops the server with the setting's name, even with no relay
  configured, instead of failing at the first reset mail. The sender is
  written in canonical form, so a display name may now appear quoted
  (`"Imvault" <no-reply@example.com>`); mail clients show it the same.
  `IMVAULT_SMTP_TLS` is still refused unless it is exactly `starttls`,
  `implicit` or `none`, and the mail transport itself now refuses an unknown
  mode too rather than treating it as plain text.
- **No mail is logged without a relay.** An instance with no
  `IMVAULT_SMTP_HOST` used to log each message it could not send, reset and
  confirmation links included. It now logs nothing about them; administrators
  issue reset links from the admin pages as before.
- **`IMVAULT_TRUST_PROXY_HEADERS` is refused; rename it to
  `IMVAULT_TRUSTED_PROXIES` before upgrading.** The old setting believed
  `X-Forwarded-For` and `X-Forwarded-Proto` from every peer. The new one names
  the reverse proxies whose forwarding headers are believed, as
  comma-separated addresses or CIDR prefixes; headers from any other peer are
  ignored. For a proxy on the same host, as in the shipped examples, replace
  `IMVAULT_TRUST_PROXY_HEADERS=true` with
  `IMVAULT_TRUSTED_PROXIES=127.0.0.1/32,::1/128`; for a proxy in another
  container or host, list its address, as `contrib/caddy/docker-compose.yml`
  now does. With `IMVAULT_TRUST_PROXY_HEADERS` set to any value, even `false`
  or empty, the server stops at startup with a message naming the
  replacement, so remove it where it was `false` too. An entry in
  `IMVAULT_TRUSTED_PROXIES` that is not an address or prefix also stops the
  server. `proxy-config` now prints the new setting. The Debian package does
  not edit `/etc/imvault/imvault.env`, so rename the setting there by hand.
- **Client addresses are read more strictly.** Behind trusted proxies, Imvault
  walks `X-Forwarded-For` from the right past each trusted proxy and takes the
  first address that is not one; an entry that is not a plain IP address
  makes it fall back to the proxy's own address. `X-Real-IP` is no longer read.
- **Rate limits group IPv6 clients by /64**, so one host cannot claim a fresh
  sign-in or upload budget for each of its addresses. Each limiter also tracks
  at most 10,000 callers, forgetting the least recently seen, so a flood of
  addresses cannot grow its memory without bound.
- **Sandbox changes** (`imvault sandbox`). `sandbox --check` and the start-up
  check now run the confined binary itself (`imvault --help`) inside the
  namespaces instead of the host's `true`, so a policy that cannot start
  Imvault fails the check. An `SSL_CERT_FILE` in the service environment now
  names the CA bundle the server trusts, which must be an existing regular
  file; it was ignored before. On merged-`/usr` systems `/bin`, `/lib` and
  similar links are recreated as links instead of separate mounts. A
  `--read-file` path must now be clean, as `--write-dir` paths already were.

