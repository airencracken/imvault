# Optional Comfyware connections

Each Comfyware app keeps its own accounts, permissions, sessions and data. With
no connection settings, Imvault uses local password sign-in and hides the Apps
menu. The app switcher never discovers or contacts other apps.

The four settings below default to empty. For Compose, put them in the project
`.env` file or shell environment. For OpenRC or systemd, edit the installed
service configuration and restart the service.

## Apps menu

Set `IMVAULT_APPS` to a JSON array of app IDs and absolute HTTP(S) addresses.
Imvault leaves itself out of the menu.

```sh
IMVAULT_APPS='[{"id":"imvault","url":"https://photos.example.org"},{"id":"songstead","url":"https://music.example.org"},{"id":"witmoot","url":"https://discuss.example.org"},{"id":"folkweave","url":"https://people.example.org"},{"id":"tidings","url":"https://read.example.org"}]'
```

Startup rejects unknown IDs, duplicate apps, and addresses with credentials,
queries or fragments. A menu entry does not grant membership in the other app.

## Community sign-in

Point Imvault at an OpenID Connect provider:

```sh
IMVAULT_SHARED_ISSUER=https://identity.example.org
IMVAULT_SHARED_CLIENT_ID=imvault
IMVAULT_SHARED_CLIENT_SECRET=configure-in-your-protected-service-environment
```

Set `IMVAULT_BASE_URL` to the public address and register exactly
`https://your-app.example.org/auth/shared/callback` with the provider. Give each
app its own client registration. The issuer must use HTTPS, except HTTP loopback
issuers for local testing. Protect the client secret like any other secret.
Imvault does not contact the provider at startup.

To link an identity, sign in with your password, open **Account**, choose
**Community sign-in** and connect. After that, the login page offers it.

- An unlinked identity cannot create an account or gain permissions from email
  or provider claims. Invitations and local restrictions still apply.
- Imvault still asks for your second factor if you have one.
- Disabled or suspended accounts cannot sign in.
- Disconnecting leaves your password working.
- Signing out ends only the Imvault session, not other apps or the provider.

Keep a working local owner password for recovery and provider outages.

Identity links live in the SQLite database and are included in full backups,
not in personal exports. A pending sign-in expires after five minutes and is
lost on restart. Run one Imvault process per database.

## Album discussions

Imvault can prepare a draft for your Witmoot server. Before you leave, it shows
what the draft includes: an album link, without the private title or contents.
In Witmoot, choose a board or conversation and check its audience before
posting.

To come back with the thread link, Witmoot needs Imvault's public address in
`WITMOOT_APPS` and its own `WITMOOT_BASE_URL`. After posting, choose **Return and
keep discussion link**, then confirm the save in Imvault. The return works only
from your original session and album, expires after twenty minutes and is lost
on restart. It never changes the album's audience. If it expires, paste the
thread URL by hand. See [album discussions](discussions.md) for the details.
