# Accounts

How people join, sign in, and leave, and what each role can do.

## Provisioning an administrator

Create administrators on the server with the installed binary. Use the same
data directory and database settings as the service:

```bash
imvault create-admin --username marcus --password-prompt --email you@example.com
```

The hidden prompt asks for the password twice. Passwords need at least eight
characters and at most 72 bytes. Email is optional. For scripts, pass one
password line on stdin:

```bash
imvault create-admin --username marcus --password-stdin --email you@example.com \
	< /path/to/admin-password
```

On a native OpenRC or systemd install, run the command as root. It reads
`IMVAULT_DATA_DIR` and `IMVAULT_DB` from the service configuration when the
environment does not set them, and writes the database as the service user:

```bash
sudo /usr/local/bin/imvault create-admin \
	--username marcus --password-prompt
```

Use `/usr/bin/imvault` for an ebuild or `make install PREFIX=/usr`. If the
service uses a custom database path, set `IMVAULT_DB` for this command too. The
service account must exist and own the data directory. For a new install:

```bash
sudo install -d -o imvault -g imvault -m 0700 /var/lib/imvault
```

In Docker, run `docker exec -it imvault /usr/local/bin/imvault create-admin
--username marcus --password-prompt`, or use `docker exec -i` with
`--password-stdin` from a script.

The command works before first start or while the service runs. It always
creates a new account: an existing username, in any capitalization, is an
error, and it never resets a password or promotes anyone. Sign in at `/login`
afterwards.

Web registration always creates ordinary members, even on an empty instance.
For browser-based first setup, see [Setup](setup.md).

## Profiles and avatars

Open **Account → Profile** (`/settings/profile`) to add a name, a plain-text
bio, and up to five labeled HTTP or HTTPS links. Every field is optional. Your
username stays the same.

Profiles are visible to signed-in members only. File and album author names link
to them, even on public pages. Private uploads, your email address and your
credentials never appear on a profile.

On the same page you can upload or remove an avatar: PNG, JPEG or GIF, up to
2 MiB and 512 by 512 pixels, with at most 64 GIF frames. Imvault re-encodes it
and removes embedded metadata. Avatars appear on member profiles and on file
contributor rows, and only signed-in members can load them. Administrators can
remove an avatar from the member's profile.

Untick **Show animated avatars** to see still pictures instead. Your device's
reduced-motion setting does the same.

## Times and dates

Times show in UTC until you choose a timezone. On `/settings/account`, find
**Times and dates**, enter a timezone such as `America/Los_Angeles`, and choose
**Save timezone**. The choice follows your account to every device. With
JavaScript on, **Use this device's timezone** fills in the field for you; it
still waits for you to save.

Admin dashboard sign-ins and backup times use your timezone. Reporting days stay
in UTC. Camera dates without a recorded offset show exactly as the camera wrote
them.

## Theme

Choose **System**, **Light** or **Dark** from **Theme** in the Account menu, or
in the header when signed out. System follows your device. An explicit choice
is remembered in this browser, before and after you sign in.

## Renaming uploads

Open a photo, animation or clip and expand **Rename** beneath its name. The new
name appears in galleries, search, downloads and the API. Links, tags, albums
and stored content stay the same. Owners and administrators can rename a file.

## Descriptions

Choose **Add description** beneath a file's name to save up to 1,000 characters
of plain text. Line breaks are kept; HTML and Markdown show as text. Choose
**Edit description** to change it, or save it empty to remove it. Owners and
administrators can edit descriptions.

Anyone who can view the file can read its description, even when camera details
are hidden. Descriptions are included in the API and account exports.

## Favorites

When signed in, choose **Favorite** on a file to save it to `/favorites`. Choose
**Favorited** to remove it. The page lists the newest favorites first and
supports search.

Favorites are private to you. They point at the file, so they never change its
visibility or expiry. A file you can no longer see drops off the list. Removing
a favorite never deletes the upload.

Gallery and Favorites search match file names, file IDs, and tag names or
slugs.

## Exporting an account

Choose **Download export** on `/settings/account` to get a zip of everything you
have uploaded. Each original sits at `files/<id>/<name>`. A `manifest.json`
lists names, descriptions, sizes, dimensions, content hashes, visibility, tags,
albums and dates. The manifest carries both `visibility` and the older `public`
boolean for existing readers. Your profile, avatar and avatar preference are
included even if you have no uploads.

The zip is built in the system temporary directory (`TMPDIR`) before the
download starts, so the server needs enough free space there. If an original is
missing or storage fails, you get an error instead of a partial zip. Only one
export runs at a time.

Exports never contain passwords, sessions, API keys, recovery codes, the
two-factor secret, or anything from another account. Thumbnails and previews
are left out; Imvault rebuilds them from the originals.

## Invitations

Administrators issue invitation codes at `/admin/invites`, with an optional
label, use limit and expiry. Each use admits one account. A blank use limit
means one use; `0` means unlimited. A blank expiry means the code never expires.

A valid invitation admits people even when registration is closed:

| `IMVAULT_ALLOW_SIGNUP` | `IMVAULT_INVITE_ONLY` | Who can register |
| --- | --- | --- |
| `true` | `false` | Anybody |
| `true` | `true` | Only with an invitation |
| `false` | either | Only with an invitation |

A code is shown once, when you create it. If it is lost, revoke it and issue
another; the list shows each code's prefix. Revoking takes effect at once. A
registration that fails, for example on a taken username, does not use up the
code.

To let a member invite people, choose **Allow invites** for them under
**Admin → Users**. They get a **can invite** badge and an **Invitations** link
on their next page load. They can create, list and revoke their own codes at
`/invites`. Each of their codes admits 1 to 25 accounts and lasts at most 30
days, which is also the default. People they invite join as ordinary members
without invitation access.

**Remove invite access** stops the member issuing codes and revokes their open
codes; granting access again does not restore them. Disabling the member
suspends their codes until they are enabled again. Deleting any account revokes
its open codes.

Each account records who invited it, with a snapshot of the inviter's username,
so administrators can trace invitations after renames or deletions.

## Signing in with a provider

An instance can trust one OpenID Connect provider, such as Keycloak, Authentik,
Google or Discord. See
[Configuration](configuration.md#signing-in-with-a-provider) for the settings.
For community sign-in shared across Comfyware apps, see the
[suite guide](suite.md).

If your account has a password, a provider is an extra way in. Your password
keeps working, so a provider outage never locks you out.

An account created through a provider has no known password. Where settings ask
for your current password, choose **Confirm with** your provider instead. For
ten minutes in that browser session, those settings work without a password.
Use the window to set one at `/settings/password`. Until you do, you cannot
disconnect your last provider.

How a provider sign-in finds an account:

- **A linked provider account signs straight in.**
- **An email address links only when both sides have verified it.** The
  provider must send `email_verified`, and the local account must have
  confirmed the same address. Otherwise the person registers a new account.
- **A shared address links nothing.** If two local accounts use the address,
  sign-in is refused with an explanation.
- **A first sign-in follows the registration policy.** On a closed or
  invitation-only instance, the person still chooses a username and enters an
  invitation code.

To connect a provider to your account, sign in first and use **Connect** on
`/settings/account`. The provider attaches to the account you are signed in as.

## Roles

Web registrations create members. Administrators change roles at `/admin/users`:

| Role | What it adds |
| --- | --- |
| **Member** | Manages their own uploads and contributes to shared albums |
| **Moderator** | Sees and removes anybody's content, and works the report queue |
| **Administrator** | Also manages accounts, roles, mail and instance settings |

Before you appoint a moderator:

- **Moderators see everything**, including private files and albums. On a
  family instance, one administrator and no moderators is common.
- **Moderators cannot change sharing.** Only the owner or an administrator can
  change a file's visibility or an album's visibility and access.
- **Moderators stay out of administration.** They reach `/admin/files` and the
  report queue, nothing else.

Imvault refuses to demote or delete the last administrator.

## Reports and the moderation log

Members can report a file or album that belongs to someone else, choosing a
reason and adding an optional note. Moderators and administrators work the queue
at `/moderation`, oldest first: remove the content or dismiss the report, with a
note either way.

Each person can have one open report per item. Once a report is closed, a new
one is allowed. If two moderators act on the same report, the second is told
someone got there first. Moderators remove content directly and have no report
button.

`/moderation/log` records every removal of someone else's content and every
report decision: who acted, what they did, to what, and why. Names are stored as
they were at the time, so entries survive account and content deletion. Owners
deleting their own uploads are not logged.

## Two-factor authentication

Turn it on at `/settings/2fa` with any TOTP app: scan the QR code or type the
key. You get ten single-use recovery codes, shown once. Keep them somewhere
safe.

Turning off two-factor, or issuing new recovery codes, asks for your password
and a current code. If you lose both your app and your recovery codes, an
administrator can clear your second factor from `/admin/users`.

Operators: TOTP secrets are encrypted with `secret.key`. Back it up with the
database; see [Security](security.md#credentials-are-stored-as-digests) and
[Operations](operations.md).

## Password reset

With `IMVAULT_SMTP_HOST` and `IMVAULT_BASE_URL` set, `/forgot` takes a username
or address and emails a single-use link. The page answers the same way whether
or not the account exists.

Without mail, `/forgot` says so, and an administrator issues a link from
`/admin/users`. The link appears once on the page and is not stored.

Each link works once, and issuing a new one cancels the old one.

Imvault queues mail in the database before sending, so a relay outage or a
restart delays messages without losing them. Failed sends retry after about 1
minute, 4 minutes, 16 minutes, 1 hour and 4 hours. After
`IMVAULT_MAIL_MAX_ATTEMPTS` tries (default 5), the message appears as failed at
`/admin/mail`, where an administrator can retry or discard it. Imvault clears a
message's body once the relay accepts it, and deletes delivered and failed
messages after a week.

Changing your password signs out your other devices. Your API keys keep working.

Email confirmation is optional and gates nothing. It shows administrators who
has confirmed an address.

## Disabling and deleting accounts

Disabling an account ends its sessions and revokes its API keys at once.

You can delete your own account from `/settings/account`. Enter your password,
your second factor if you have one, and your username. Deletion removes every
upload and its stored files, your avatar and your preferences. Administrators
can delete accounts from `/admin/users`. The last administrator cannot be
deleted.
