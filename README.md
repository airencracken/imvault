# imvault

A small, self-hosted image and clip host in the spirit of MLKSHK, Picsur,
[Chibisafe](https://github.com/chibisafe/chibisafe), and
[Imgur](https://imgur.com) — a Go server with a server-rendered, htmx-driven
front end. No build step, no JavaScript framework, no external services: one
binary and a data directory.

```
make demo          # a throwaway instance, seeded with sample media
```

The first account you register becomes the administrator.

It serves three scales from the same binary, because they are settings rather
than different programs: a **personal** host for one person, a **group** host
for a community, and a **public** host open to the world. The settings page
offers those three as profiles and lets you tune each axis by hand. See
[Configuration](docs/configuration.md#instance-settings).

## What it does

**Media.** Still images (JPEG, PNG, WebP, BMP, TIFF), animated GIFs and WebPs,
and short WebM, MP4, or MOV clips. Uploads are classified from their magic
bytes, not their filenames, and validated by actually decoding them. Thumbnails
and previews are generated once, with EXIF orientation applied and transparency
preserved. Animations are never re-encoded and clips are never transcoded: the
originals are served, with range requests. Photograph metadata — the camera, the
date, and the coordinates a phone writes — is removed from what a public file
serves, per file and per album, without re-encoding the picture, and what
survives is shown behind a collapsed disclosure with the location withheld from
anybody the file is not shared with. ffmpeg is optional, and without it
clips get a placeholder poster rather than failing. See
[Media handling](docs/media.md).

**Library.** Per-account galleries, albums, and tags, with tags scoped to the
account that owns the file and a shared namespace for anonymous uploads. A
members feed shows what everybody has shared, not only what you put there. Every
file and album is public, members-only, or private, and an album can be shared
so that anybody on the instance may add their own images to it. Anonymous
uploads with a retention window enforced by a reaper.

**Accounts.** Multi-user signup, invitation codes so a closed instance is not an
unreachable one, three roles so moderation can be somebody else's job without
handing them the instance, two-factor authentication with recovery codes,
sign-in through any OpenID Connect provider, password reset by email or by an
administrator-issued link, and self-service account deletion. See
[Accounts](docs/accounts.md).

**API.** Bearer-token keys with full CRUD over files, albums, and tags, taking
JSON or form encoding, and returning bare URLs for tools that want them. See
[API](docs/api.md).

**Moderation.** Members can report a file or an album; reports land in a queue a
moderator works, and every removal of somebody else's content is recorded with
who did it, when, and why. See [Accounts](docs/accounts.md).

**Operations.** Per-account storage quotas and single-file limits, an
instance-wide storage ceiling, a bound on how many uploads are processed at
once, identical uploads stored once, an export of everything an account holds,
upload and sign-in rate limiting, an admin area for usage, quotas, user
management, and content moderation, and outbound mail that is queued and
retried rather than dropped. See [Configuration](docs/configuration.md) and
[Operations](docs/operations.md).

## Quick start

### Try it in one command

```bash
make demo
```

Builds the server, boots it on <http://localhost:8080> against a throwaway
`./demo-data` directory, and seeds a small instance: three accounts with
different roles, photographs at each visibility, an album two of them have
contributed to, tags, an invitation, and a report in the queue alongside one a
moderator has already dealt with. It prints the sign-ins, an API key, and a
short list of what is worth looking at, then runs in the foreground until you
press Ctrl-C. `make clean-demo` removes the data.

Everything is seeded through the same HTTP surface a person or a script uses —
the signup form, the API, the report form, the admin pages — so what you see is
what those paths actually do.

```bash
PORT=9000 make demo      # if 8080 is taken
make help                # every other target
```

### From source

```bash
make run                             # :8080, using ./data
make run PORT=9000 DATA_DIR=/tmp/imv # somewhere else
```

Or without make:

```bash
go build -o bin/imvault ./cmd/imvault
IMVAULT_DATA_DIR=./data ./bin/imvault
```

Building needs Go 1.26 or later, because that is what the dependencies require.
The binary has no cgo dependency, so it cross-compiles and runs on a scratch
image.

### Docker

```bash
docker build -t imvault .
docker run -d --name imvault \
  -p 8080:8080 \
  -v imvault-data:/data \
  -e IMVAULT_ALLOW_SIGNUP=false \
  imvault
```

The image bundles ffmpeg. Remove `ffmpeg` from the `apk add` line in the
`Dockerfile` to slim it down.

### Docker Compose

A `docker-compose.yml` is included, with every setting overridable from the
environment or a `.env` file:

```bash
make compose-up      # or: docker compose up --build -d
make compose-down
```

### On a server, without Docker

```bash
make dist                        # dist/imvault-<version>.tar.gz
sudo make install PREFIX=/usr    # /usr/bin/imvault
sudo make install-systemd        # or: install-openrc
```

`PREFIX` defaults to `/usr/local`, but the OpenRC script looks for the binary at
`/usr/bin/imvault`, which is where a package installs it; `make install-openrc`
says so when the two disagree.

Both init systems are supported, with an APKBUILD for Alpine and a live ebuild
for Gentoo alongside them. For TLS, `contrib/caddy/` has a Caddy configuration
that obtains and renews certificates on its own. See
[Deployment](docs/deployment.md).

## Before you run this

Two things worth reading before an instance has anything in it:

- **Back up `secret.key` with the database.** They are two halves of the same
  thing. Restoring one without the other looks fine until somebody with
  two-factor authentication cannot sign in. See
  [Operations](docs/operations.md).
- **Put it behind TLS and set `IMVAULT_SECURE_COOKIES=true`.** Without it, a
  session cookie can be read off a plaintext connection.

[Security](docs/security.md) covers the rest: how credentials are stored, the
visibility model, and what is deliberately not defended against.

## Documentation

| | |
| --- | --- |
| [Configuration](docs/configuration.md) | Every setting, quotas, and rate limiting |
| [Accounts](docs/accounts.md) | Sign-in, two-factor, password reset, deletion, administration |
| [Media handling](docs/media.md) | What is accepted, and what it becomes |
| [API](docs/api.md) | The programmatic API |
| [Routes](docs/routes.md) | Every URL the server answers |
| [Deployment](docs/deployment.md) | systemd, OpenRC, packages, and logs |
| [Operations](docs/operations.md) | Backups, maintenance, upgrading, troubleshooting |
| [Security](docs/security.md) | The security model, and its edges |
| [Architecture](docs/architecture.md) | Internals, design decisions, and development |

## Limitations

Worth being upfront about what this deliberately does **not** do yet:

- No transcoding. Clips are stored exactly as uploaded, so whatever the browser
  supports is what you get. An MP4 with an exotic codec will not play.
- Animated WebP thumbnails depend on `golang.org/x/image/webp` decoding the first
  frame; if it cannot, the tile falls back to a placeholder poster.
- No video streaming beyond HTTP range requests — no HLS or adaptive bitrate.
- Tags cannot be applied to anonymous uploads: those files have no owning account
  for a tag to live in.
- API keys can manage files, albums, and tags, but not account settings
  (password, email), or key management. No admin operations are exposed over the
  API either — the admin area is web-only.
- Password reset by email needs a working relay; an instance without one falls
  back to administrator-issued links.
- The mail queue is swept on a timer, so a message queued just after a sweep
  waits up to `IMVAULT_MAIL_RETRY_INTERVAL` before its first retry.
- Rate limiting is in-memory and per process, so running several instances behind
  a load balancer gives each its own budget.
- Storage usage can drift if the process is killed mid-upload; the admin
  dashboard has a button to recalculate it from the files table.
- Two-factor authentication is TOTP only: no WebAuthn or hardware keys, and the
  recovery codes are the sole fallback.

## License

Copyright (C) 2026 Marcus J. Hildum

This program is free software: you can redistribute it and/or modify it under
the terms of the **GNU Affero General Public License** as published by the Free
Software Foundation, either version 3 of the License, or (at your option) any
later version.

It is distributed in the hope that it will be useful, but **without any
warranty**; without even the implied warranty of merchantability or fitness for
a particular purpose. See the [GNU AGPL](LICENSE) for details.

Because the AGPL covers software used over a network, running a modified version
as a service obliges you to offer its users the corresponding source. The footer
carries a source link for exactly that reason; point `IMVAULT_SOURCE_URL` at your
own fork if you have modified it, or set it to empty to remove the link.

Every source file carries an `SPDX-License-Identifier: AGPL-3.0-or-later` tag.
