# Album discussions in Witmoot

Set `IMVAULT_WITMOOT_URL=https://board.example.org` and
`IMVAULT_BASE_URL=https://photos.example.org`, then restart. Both applications
remain independent; there is no shared database, sign-in or automatic thread.
The optional destination is validated without contacting it during startup.

On an album page, signed-in readers can explicitly prepare a Witmoot draft.
The handoff carries only the album link, without its title, cover, count,
description or contents. Witmoot asks for a board or existing topic, shows that
conversation's audience, and requires the ordinary explicit posting action.
Sign in to Witmoot first or reopen the handoff after signing in.
The album's own visibility and each file's access rules remain authoritative.

An album owner can save or remove an existing discussion link. Only readers
allowed to see the album see those links. Album deletion deletes its discussion
references but leaves Witmoot threads and album files untouched. No references
are resolved server-side, and a Witmoot outage never affects album viewing.

Witmoot's optional imvault connection can request a current public album preview
using `GET /api/v1/albums/{ref}/preview`. This endpoint always requires bearer
authentication and the existing owned-album check. It returns 404 for a private
or members-only album, without title, counts, cover or descriptions. A public
response contains title, slug, visibility, public image count and destination
URL. Counts exclude private/member files, expired files and videos. There is
no unauthenticated metadata endpoint and no copied album contents. Witmoot checks
local post access before requesting it and discards nonpublic responses.

Schema migration `030_album_discussions.sql` adds the link table only. Stop the
service and back up the complete data directory before running the existing
`imvault migrate` workflow. Restoring an old backup permits downgrading; opening
a migrated database with an old binary does not. Existing album APIs are unchanged.
Publish Comfylib v0.1.1 (including `reference`), resolve verified module checksums,
and run clean release checks with GOWORK disabled before publishing this version.
