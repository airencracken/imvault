# Optional Comfyware connections

Each app keeps its own accounts, permissions, sessions and data. All connections
are optional. An empty configuration retains local password sign-in and hides
the Apps menu. No destination is discovered or contacted by the app switcher.

## Apps menu

Set `IMVAULT_APPS` to a JSON array of known app IDs and absolute HTTP(S)
addresses. The current app is omitted automatically. For example:

```sh
IMVAULT_APPS='[{"id":"imvault","url":"https://photos.example.org"},{"id":"songstead","url":"https://music.example.org"},{"id":"witmoot","url":"https://discuss.example.org"},{"id":"folkweave","url":"https://people.example.org"},{"id":"tidings","url":"https://read.example.org"}]'
```

Use the addresses served by your own installation. Unknown IDs, duplicate apps,
credentials, queries and fragments are rejected at startup. The menu does not
provide membership in another app.

## Community sign-in

Configure an OpenID Connect provider using:

```sh
IMVAULT_SHARED_ISSUER=https://identity.example.org
IMVAULT_SHARED_CLIENT_ID=imvault
IMVAULT_SHARED_CLIENT_SECRET=configure-in-your-protected-service-environment
```

Also configure the app's existing `IMVAULT_BASE_URL` to its public address and
register exactly `https://your-app.example.org/auth/shared/callback` with the
provider. Each app uses its own client registration. The issuer must use HTTPS;
HTTP loopback issuers are accepted for local testing. Protect the client secret
like the existing application secrets. No provider request is made at startup.

Sign in with a local password, open Account, choose Community sign-in, and
connect your community identity. Later, the login page can use that identity.
An unlinked identity cannot create an account or inherit permissions from email
or provider claims. Invitations and local owner/member restrictions still apply.
Imvault also requires its existing second factor when the local account has one.
Disabled or suspended accounts cannot sign in. Disconnecting the identity leaves
the local password usable. Signing out ends the session in this app; it does not
sign out other apps or the provider. Keep a working local owner password for
recovery and provider outages.

Bindings are stored in the application's SQLite database and included in full
database backups. Personal exports do not copy identity bindings to other apps.
Pending sign-ins expire after five minutes and are lost on server restart.
Use a single application process per database, as required by the existing
instance lock; pending flows are intentionally held only in that process.

## Discussions and privacy

Songstead and Imvault can prepare drafts for their configured Witmoot server.
The source app shows the information included before leaving. Imvault includes
an album link without its private title or contents. In Witmoot, choose a board
or existing conversation and review its audience before posting.

For the return flow, configure the source app's public address in `WITMOOT_APPS`
and configure Witmoot's public `WITMOOT_BASE_URL`. After posting, choose Return
and keep discussion link. The source app asks you to confirm saving it through
its normal authenticated form. The return is bound to the original local session
and source item, expires after twenty minutes and is lost on server restart.
It never changes either item's audience. Keep an existing thread URL manually if
a return expires. App links, album previews, local comments and shared sign-in
remain independently configurable.

## Reading and controls

Account actions, invitations, host tools and theme selection are grouped under
Account. Native disclosure controls also work without JavaScript. Optional upload
metadata and music details can be expanded when needed; audience choices remain
visible. Songstead and Tidings offer comfortable or compact reading. That choice
is stored per app in this browser, synchronizes between its tabs and falls back
to comfortable if browser storage is unavailable. Other browsers keep their own
choice. Imvault uses neutral privacy labels; each upload still has an explicit
audience.

## Shared source maintenance

`internal/suite`, `internal/sharedoidc`, `comfy-components.css` and
`comfy-suite.js` are synchronized from Comfylib. They are checked by
`python3 scripts/verify_suite.py` and the normal `make check`. Applications still
build independently against published Comfylib releases. To update the shared
sources from a Comfylib checkout, run `python3 tools/sync_suite.py /path/to/imvault`;
use `--check` to compare against the canonical sources without writing. Review
the source changes and generated hash manifest together.

Native service templates and Compose forward the four optional suite settings.
For Compose, put them in the project `.env` file or shell environment; for OpenRC
or systemd, edit the installed service configuration and restart the service.
All four default to empty, leaving navigation and community sign-in disabled.
