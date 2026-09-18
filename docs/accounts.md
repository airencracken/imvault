# Accounts

Registration, signing in, second factors, and the way out.

## Exporting an account

`/settings/account` downloads a zip of everything the account has uploaded,
plus a `manifest.json` describing it: names, sizes, dimensions, content hashes,
each file's visibility, tags, albums, and the dates. Each original sits at
`files/<id>/<name>`, so two uploads called `photo.jpg` do not collide.
The manifest carries both `visibility` and the older `public` boolean, so a
reader written against two levels keeps working.

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

## Invitations

An instance can be open to anybody who finds it, closed to everybody, or
somewhere in between. Invitations are the in-between: a code an administrator
issues at `/admin/invites`, with an optional label, a use limit, and an expiry,
that admits one account each time it is used.

An invitation **always** admits, even when registration is switched off
altogether. It is a deliberate grant rather than a public door, and without that
the only way to add somebody to a closed instance would be to edit the database
by hand. The two switches compose:

| `IMVAULT_ALLOW_SIGNUP` | `IMVAULT_INVITE_ONLY` | Who can register |
| --- | --- | --- |
| `true` | `false` | Anybody |
| `true` | `true` | Only with an invitation |
| `false` | either | Only with an invitation |

A code is shown once, when it is created, and only a digest is stored — the same
trade API keys make. If one is lost, revoke it and issue another; the list shows
each code's prefix so it is clear which row is which. Revoking takes effect
immediately.

Issuing a code with several uses is for a group you trust together, and `0`
means no limit at all. A code's uses are consumed in the same transaction that
creates the account, so a registration that fails — a username that is already
taken, say — does not cost the code a use.

## Signing in with a provider

An instance can trust one OpenID Connect provider — Keycloak, Authentik,
Google, Discord, or anything else that speaks it — configured with
`IMVAULT_OIDC_ISSUER` and friends. See
[Configuration](configuration.md#signing-in-with-a-provider) for the settings.

Sign-in is **supplementary**. Passwords keep working, every account keeps one,
and connecting a provider never removes the ability to sign in without it. A
provider being down is an inconvenience rather than a lockout.

Three decisions are worth knowing about, because each is a place where trusting
a provider too readily would hand over an account:

- **An address only links when the provider says it has verified it.** Most
  providers let an account set any address it likes; `email_verified` is the
  claim that separates "this is their address" from "this is a string they
  typed". Without it, a provider that does not check addresses would become a
  way to sign in as whoever you name.
- **An ambiguous address links nothing.** Addresses are not unique here, so if
  two accounts share one, signing in with the provider is refused with an
  explanation rather than guessing which was meant.
- **A first sign-in still passes through the instance's own policy.** If
  registration is closed or invitation-only, the provider does not open a second
  front door: after the provider verifies the identity, the person is asked for
  a username and an invitation code, exactly as an ordinary registration would
  be.

Signing in with a provider whose subject is already linked goes straight
through, since that is what the link is for. Connecting a provider while signed
in attaches it to **the account you are signed in as**, never to whoever the
provider happens to name, so the account page is the safe way to add one.

## Roles

The first account registered is the administrator. After that, every account has
one of three roles, set from `/admin/users`:

| Role | What it adds |
| --- | --- |
| **Member** | Manages its own uploads, and contributes to shared albums |
| **Moderator** | Can see and remove anybody's content, and work the report queue |
| **Administrator** | Can also manage accounts, roles, mail, and instance settings |

The split is deliberate. Moderation has to scale with the group, so it is a role
somebody else can hold; account administration does not, and a moderator who
could edit accounts could promote themselves and take the instance. A moderator
therefore reaches `/admin/files` and the report queue, and nothing else in the
admin area.

Two things are worth knowing before granting it:

- **A moderator can see everything.** Judging content you are not allowed to
  look at is not possible, so the role includes visibility of private files and
  albums. On an instance where that is not acceptable, do not have moderators —
  a family instance with one administrator is the normal shape.
- **A moderator cannot republish.** Changing a file's visibility or an album's
  visibility and sharing is the owner's and the administrator's. Removing a bad
  upload is a moderation action; making a private upload public is not, and a
  moderator who could do it would be an escalation rather than a safeguard.

Two operations are refused to avoid locking everybody out: demoting the last
administrator, and deleting the last administrator.

## Reporting, and the moderation log

A signed-in member can report a file or an album that is not theirs, with a
reason and an optional note. Reports land in a queue at `/moderation`, oldest
first, which moderators and administrators can work: remove the content, or
dismiss the report, with a note either way.

A few decisions worth knowing about:

- **Reporting is for members, not moderators.** A moderator looking at a bad
  upload can remove it, so offering them a report button would be busywork. The
  control is not shown to them, and the route refuses them.
- **Reporting is for other people's content.** Your own upload has a Delete
  button, and the route says so rather than accepting a report against yourself.
- **One open report per person per target.** Reporting something twice is not
  more persuasive, and a queue that fills with duplicates is a queue nobody
  works. A second person's complaint is a separate report, and a new report is
  allowed once an earlier one has been closed either way.
- **Closing a report happens once.** The status condition is part of the
  statement, so two moderators working the same queue cannot both act on the
  same report; the second is told somebody got there first.

Every removal of somebody else's content is recorded in the log at
`/moderation/log`, along with the report decisions. Each entry names the person
who acted, what they did, the thing they acted on, and why. The names are
snapshots rather than references: deleting the account or the content does not
erase the record, because a log that turns into a list of numbers once somebody
leaves is not an audit trail.

An owner clearing out their own gallery is **not** logged. That is housekeeping
rather than moderation, and filling the log with it would bury the entries that
matter.

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
