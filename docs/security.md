# Security

What Imvault protects, and what it leaves to you. Read
[What is not defended against](#what-is-not-defended-against) first.

## Credentials are stored as digests

| Credential | Stored as |
| --- | --- |
| Passwords | bcrypt |
| Session tokens | SHA-256 digest of a 256-bit random token |
| API keys | SHA-256 digest, found by a public prefix |
| Password reset and email confirmation tokens | SHA-256 digest |
| Recovery codes | SHA-256 digest |
| **TOTP secrets** | **AES-GCM encrypted** |

Imvault must read a TOTP secret back to check a code, so it encrypts the secret
with a key in `secret.key` beside the database. A stolen database alone yields
no usable second factors. Back up the key with the database; without it, every
account with two-factor needs a recovery code or an administrator. Set
`IMVAULT_SECRET_KEY` to supply the key from your own secret store. See
[Operations](operations.md).

Account exports never include password hashes, TOTP secrets or API keys.

## Two-factor authentication

- **Codes cannot be replayed.** A code seen over a shoulder is useless for the
  rest of its window.
- **Removing two-factor takes both factors.** Turning it off, or reissuing
  recovery codes, asks for the password and a current code.
- **The code prompt lasts five minutes.** A correct password starts it, and a
  mistyped code can be retried within that time.
- **Every way in asks for the code.** Password resets and provider sign-ins
  reach the same prompt before a session starts.
- **Turning two-factor on signs out other sessions.**
- **Only administrators can clear a second factor**, from `/admin/users`.

A password change revokes older sessions, reset links and pending sign-ins.

## Cross-site request forgery

Every request that changes something and uses a cookie must carry a CSRF token,
in the `X-CSRF-Token` header (htmx sends it) or a `csrf_token` form field. In a
multipart form, the token must be the first field; Imvault reads no upload
without it. Signed-in tokens are tied to the session. Signed-out forms, such as
sign-in, use a double-submit cookie.

`/api/v1/...` endpoints are exempt. They accept only bearer keys, which a
browser never attaches on its own.

Status messages after a redirect ("Album saved.") are signed, so a crafted link
cannot put words in the site's mouth.

## Cookies and headers

Cookies are `HttpOnly` and `SameSite=Lax`. They are `Secure` when
`IMVAULT_SECURE_COOKIES` is set, or when a proxy listed in
`IMVAULT_TRUSTED_PROXIES` reports `X-Forwarded-Proto: https`. Turn this on
behind TLS.

Secure session and CSRF cookies use the `__Host-` prefix
(`__Host-imvault_session`, `__Host-imvault_csrf`), so a sibling subdomain or a
plain-HTTP page cannot set them. Over plain HTTP, as in local development, they
use bare names, and HTTPS requests ignore bare names.

Every response sends `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
`Content-Security-Policy: frame-ancestors 'none'` and
`Referrer-Policy: same-origin`. Uploads are served as the type Imvault checked,
and no other site can frame a page.

Links in email use `IMVAULT_BASE_URL`, never the request's `Host` header.

## Who can see what

| Level | Who can see it |
| --- | --- |
| **Public** | Anybody, signed in or not |
| **Members** | Any signed-in account |
| **Private** | The owner, administrators and moderators |

Listings and file downloads use the same check. Anonymous uploads are always
public and expire after the retention window. Non-public files are sent with
`Cache-Control: private`, so shared caches do not keep them.

An album's `access` setting decides who can add files to it. Contributors can
add and remove only their own uploads. Only the album owner or an administrator
can remove someone else's file from an album.

Missing files, other people's private files and unknown IDs all return the same
`404`, in the web pages and the API. Tags appear only for files you can see.

## Roles

| Role | Sees | Can remove | Can change visibility or sharing |
| --- | --- | --- | --- |
| Member | Their own, plus public and members-only content | Their own | Their own |
| Moderator | Everything | Anybody's | Nobody's |
| Administrator | Everything | Anybody's | Anybody's |

Moderators cannot reach accounts, instance settings, the mail queue or roles, so
a moderator cannot promote themselves. See [Accounts](accounts.md#roles).

## Reports and the audit trail

Only moderators and administrators see reports. A report keeps the reporter's
name after their account is deleted. Every removal of someone else's content is
logged with the actor, action, target and reason, using the names as they were
at the time. See [Accounts](accounts.md#reports-and-the-moderation-log).

## Signing in through a provider

Imvault trusts an OpenID Connect provider for one thing: that the person
controls an account there. The provider's subject identifies the link. An email
address links an existing account only once, on first sign-in, and only when
the provider reports it verified and exactly one local account has confirmed it.
A first sign-in still follows the registration policy, including invitation
codes.

Accounts created through a provider get a random password, never an empty one.

## Photograph metadata

Photos can carry camera details and GPS coordinates. Owners choose what other
people see; [Media handling](media.md#metadata) explains the settings.

When Imvault cannot strip metadata from a file, such as a clip on a server
without ffmpeg, it withholds the file from anyone who should not see that
metadata. It never serves the original while the page claims it is clean.

The owner and administrators always get the original: on the file page, in the
account export, and through the owner's API key.

## Rate limiting

Two in-memory budgets:

- **Sign-in**, per client address. It covers the password step, the two-factor
  prompt, registration, password resets, provider registration and the
  current-password checks in settings. A reset request answers before any mail
  is sent, so timing does not reveal whether the account exists.
- **Uploads**, per account, or per client address for anonymous uploads.

Behind a reverse proxy, list the proxy in `IMVAULT_TRUSTED_PROXIES` so Imvault
reads the real client address from `X-Forwarded-For`. Imvault reads the header
only on connections from listed proxies, walking from the right past other
trusted proxies to the first untrusted address. **List only proxies you
control**; anyone else can forge the header. `X-Real-IP` is ignored. IPv6
clients share one budget per /64.

Each server process has its own budgets.

## Service hardening

The systemd unit and OpenRC script run Imvault as an unprivileged account with no
shell. The systemd unit also makes everything except the data directory
read-only, gives the service a private `/tmp`, and blocks kernel module loading,
device access and clock changes.

The unit does not set `SystemCallFilter`, because Imvault runs ffmpeg for clip
posters and a tight filter breaks video without a clear error. Add one after
checking the journal for `EPERM` following an upload.

For further confinement, see [Bubblewrap](sandbox.md).

## Uploaded content

Imvault decodes every upload to check it, ignoring the extension and
`Content-Type`. Anything that is not a recognised image or clip is rejected.
Images over 24 megapixels are refused before decoding, which stops
decompression bombs. ffprobe measures clip duration; without it, only the size
limit applies.

## What is not defended against

- **No WebAuthn or hardware keys.** A phishing site that relays a TOTP code in
  time gets in.
- **No encryption at rest.** Anyone with filesystem access can read the
  database and uploads. Use full-disk encryption if that matters.
- **No malware scanning.** Uploads must decode as images or clips, but nothing
  inspects their content.
- **No isolation between accounts.** One instance is one trust domain, sharing
  one process and one database.
- **No tamper-proof audit trail.** The moderation log and request log record
  actions with the actor, but nothing makes them immutable.

## Reporting a problem

Open an issue at <https://github.com/airencracken/imvault/issues>. If the
details should stay private, say so in the issue without them, and we will
arrange a private channel.
