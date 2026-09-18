# Accounts

Registration, signing in, second factors, and the way out.

## Exporting an account

`/settings/account` downloads a zip of everything the account has uploaded,
plus a `manifest.json` describing it: names, sizes, dimensions, content hashes,
tags, albums, and the dates. Each original sits at `files/<id>/<name>`, so two
uploads called `photo.jpg` do not collide.

The archive is streamed as it is built rather than assembled on disk first,
because an export can easily be larger than the space left on the server.

What is deliberately **not** in it: passwords, sessions, API keys, recovery
codes, and the two-factor secret. Neither is anything belonging to another
account. The manifest is built from purpose-written types rather than from the
model structs, precisely so that adding a field to a model cannot quietly add a
credential to a download; there is a test that checks the archive for each of
them.

Renditions are not included either. They are deterministic from the originals
and the settings, so shipping them would double the size to save a few seconds
of processing.

The first account registered is the administrator; the role can be granted and
revoked from `/admin/users`. Two operations are refused to avoid locking
everybody out: demoting the last administrator, and deleting the last
administrator.

## Two-factor authentication

Any TOTP app works: the setup page shows a QR code and the key for typing in by
hand. Ten single-use recovery codes are issued when it is switched on, and they
are shown once — only digests of them are stored.

A few decisions worth knowing about:

- **The secret is encrypted, not hashed.** Sessions, API keys, and reset tokens
  are all stored as digests because the server never needs them back. A TOTP
  secret does have to be read to check a code, so it is encrypted with a key
  kept in its own file (`secret.key`) beside the database. A leak of the
  database alone therefore does not yield usable second factors.
- **Codes cannot be replayed.** The last accepted time step is recorded, so a
  code seen over a shoulder is not reusable for the rest of its window.
- **Removing the second factor needs both factors.** Disabling it, or
  reissuing recovery codes, asks for the password *and* a current code. Losing
  the app is what recovery codes are for.
- **An administrator can clear it** from `/admin/users`, which is the way back
  in for somebody who has lost both the app and the recovery codes.

> **Back up `secret.key` with the database.** They are two halves of the same
> thing: restoring the database without the key leaves every TOTP secret
> unreadable, and affected accounts need recovery codes or an administrator to
> clear their second factor. Set `IMVAULT_SECRET_KEY` instead if you would
> rather supply the key from your own secret store.

## Password reset

Two paths, chosen by whether `IMVAULT_SMTP_HOST` is set.

With a relay configured, `/forgot` takes a username or address, mints a
single-use token and emails the link. The response is identical whether or not
the account exists, so the endpoint cannot be used to enumerate accounts.

Without a relay, `/forgot` says so plainly and an administrator issues the link
from `/admin/users`. That link is rendered straight into the response rather than
redirected to, so it never lands in a URL, a browser history entry or a server
log, and it is not stored anywhere.

Either way the token is stored as a SHA-256 digest, works exactly once, is
scoped to its purpose — a confirmation token cannot open the reset form — and
issuing a new one invalidates the previous.

Mail is written to an `outbound_mail` table *before* delivery is attempted, so a
relay that is down, restarting or misconfigured delays a message rather than
losing it. A worker retries with a growing backoff (roughly 1m, 4m, 16m, 1h,
4h), and after `IMVAULT_MAIL_MAX_ATTEMPTS` the message is parked as failed and
surfaced at `/admin/mail`, where it can be requeued or discarded by hand. This
also means a message survives a restart: the queue is in the database, not in
memory. Changing a password signs out every
other device; API keys are deliberately left alone, since revoking a script's
credentials on an ordinary password change would be surprising.

Email confirmation is available but never gates anything: an unverified address
works exactly like a verified one. The flag exists so an operator can see who
has proved their address.

Disabling an account revokes its live sessions and every API key it holds, so it
loses access immediately rather than at the next login. Deleting an account
collects its storage keys *before* the database rows cascade away, so the bytes
are removed too rather than being orphaned on disk.

## Deleting an account

Any account can delete itself from `/settings/account`. It asks for the
password, a second factor when one is set, and the username typed out, because
it is irreversible and takes every upload with it. Storage keys are collected
before the database rows cascade away, so the bytes are removed rather than
orphaned on disk.

The last administrator cannot delete itself: that would leave the instance with
nobody able to administer it.
