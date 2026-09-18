# Security

What imvault does to protect an instance, and what it does not. The second list
is the more useful one.

## Credentials are stored as digests

Nothing that can be presented as a credential is stored in a form that would be
usable if the database leaked.

| Thing | Stored as |
| --- | --- |
| Passwords | bcrypt |
| Session tokens | SHA-256 digest of a 256-bit random token |
| API keys | SHA-256 digest, looked up by an indexed public prefix |
| Password reset and email confirmation tokens | SHA-256 digest |
| Recovery codes | SHA-256 digest |
| **TOTP secrets** | **Encrypted, not hashed** |

The last one is the exception, and it has to be: a TOTP secret must be readable
again in order to check a code, so it cannot be a one-way digest. It is
encrypted with AES-GCM under a key kept in `secret.key` beside the database, so
a leak of the database alone does not yield usable second factors. Back up that
key with the database, or see [Operations](operations.md) for what happens if
you do not.

A fast hash is right for the tokens and recovery codes and wrong for passwords.
The tokens are long and random, so there is nothing to guess and lookup has to
stay cheap; passwords are neither, so they get bcrypt.

## Two-factor authentication

Any TOTP app works. Ten single-use recovery codes are issued at enrolment and
shown once.

- **Codes cannot be replayed.** The last accepted time step is recorded, so a
  code seen over a shoulder is not usable for the rest of its window.
- **Removing the second factor needs both factors.** Disabling it, or reissuing
  recovery codes, asks for the password *and* a current code. Otherwise a
  borrowed session would be enough to strip the protection off an account.
- **The sign-in second step is authorised by a token, not by state alone.** A
  correct password mints a five-minute token that the code prompt presents. It
  is spent only on success, so a mistyped code can be retried without starting
  over.
- **An administrator can clear a second factor** from `/admin/users`. This is
  the way back in for somebody who has lost both their app and their recovery
  codes, and it is why it is administrator-only.

## Cross-site request forgery

Every mutating request that is authenticated by a cookie must present a
double-submit token, either as the `X-CSRF-Token` header (which htmx sends
automatically) or as a form field. The token lives in a readable cookie and must
be echoed back, so a request forged by another site cannot produce it.

The `/api/v1/...` endpoints are exempt, and that is deliberate: they are never
authenticated by a cookie. A bearer token is not ambient authority, so a
cross-site request cannot make a browser attach one.

Cookies are `HttpOnly` and `SameSite=Lax`, and `Secure` when
`IMVAULT_SECURE_COOKIES` is set. **That setting matters behind TLS**: without it
a cookie can be read off a plaintext connection.

## Who can see what

There are three levels, and they are applied by a single predicate in the store
so that a listing can never disagree with the route that serves the bytes:

| File | Who can see it |
| --- | --- |
| Public | Anybody with the link, and anybody browsing |
| Private | Its owner, and administrators |
| Anonymous upload | Anybody with the link, until its retention window closes |

Unknown ids, other people's private files, and files that never existed all
produce the same `404`. The API does the same for other accounts' resources, so
it cannot be used to discover what exists.

Tags follow the file they label. The tag index and its counts are computed over
the files you can see, so a tag that only labels somebody else's private upload
never appears, and resolving it looks exactly like a tag that does not exist.

## Rate limiting

Two budgets, both in memory, both keyed so that one client cannot exhaust
another's:

- **Sign-in**, by address and the name being tried. This covers the password
  step and the code prompt together, which matters because a six-digit code is
  small enough that unlimited guesses would eventually find one.
- **Uploads**, by account when signed in and by client address for anonymous
  uploads.

Behind a reverse proxy, every request arrives from the proxy's address, so
anonymous uploads would share one bucket. `IMVAULT_TRUST_PROXY_HEADERS` reads
the client from `X-Forwarded-For` instead — **only enable it behind a proxy you
control**, because those headers are otherwise client-supplied, and trusting
them would let a caller sidestep the limit entirely.

Both limiters are per process. Several instances behind a load balancer each get
their own budget.

## What the deployment does

The systemd unit and the OpenRC script both run the service as an unprivileged
account with no shell. The systemd unit additionally keeps everything but the
data directory read-only, gives the service a private `/tmp` for the temporary
copies ffmpeg makes, and drops the privileges that would let it load kernel
modules, create device nodes, or change the clock.

`SystemCallFilter` is deliberately not set. imvault shells out to ffmpeg for
clip posters, and a filter that turned out to be too tight would break video
silently rather than loudly. It is a reasonable addition once you have checked
the journal for `EPERM` after an upload.

## Uploaded content

Every upload is validated by decoding it, not by trusting its extension or its
`Content-Type`. Anything that is not a recognised image or clip is rejected.

Decoding happens with limits: an image above 100 megapixels is refused before a
pixel buffer is allocated, which is what stops a decompression bomb from
exhausting memory. Animated GIFs are counted by walking the block structure
rather than decoding every frame.

Clips are probed with ffprobe, which is also how the duration limit is enforced.
Without ffprobe the duration cannot be measured, and the size limit is the only
one that applies.

## What is not defended against

Worth being explicit about:

- **No WebAuthn or hardware keys.** TOTP only, so a phishing site that relays a
  code within its window is not stopped by anything here.
- **No encryption at rest for uploads.** The database and the objects are
  readable by anyone with filesystem access. Use full-disk encryption if that
  matters.
- **No malware scanning.** Uploads are validated as images, which limits what
  they can be, but nothing inspects their content.
- **No per-account isolation of the process.** One instance is one trust
  domain; every account shares the same binary and database.
- **No audit log beyond the request log.** Administrative actions are logged
  with an actor id, but there is no immutable trail.

## Reporting a problem

Open an issue at <https://github.com/airencracken/imvault/issues>. If it is
something you would rather not put in public, say so in the issue without the
details and a private channel can be arranged.
