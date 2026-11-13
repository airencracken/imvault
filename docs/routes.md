# Routes

Every URL the server answers. The API is listed in [API](api.md#endpoints).

Requests that change something and use a cookie need the CSRF token, as the
`X-CSRF-Token` header or a `csrf_token` form field (the first field of a
multipart form). `/api/v1/...` uses bearer keys and is exempt. Routes marked
*rate-limited* share the sign-in budget described in
[Security](security.md#rate-limiting).

## Public and sign-in

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/` | Landing page, or your gallery when signed in |
| `GET` | `/about` | House rules, contact details and site staff |
| `GET` `POST` | `/login` | Sign in (*rate-limited*) |
| `GET` `POST` | `/login/2fa` | The two-factor step (*rate-limited*) |
| `GET` `POST` | `/register` | Create an account (*rate-limited*) |
| `POST` | `/logout` | Sign out |
| `GET` `POST` | `/forgot` | Request a password reset (*rate-limited*) |
| `GET` `POST` | `/reset/{token}` | Choose a new password (*rate-limited*) |
| `GET` | `/verify/{token}` | Confirm an email address |
| `GET` | `/auth/oidc/start` | Start provider sign-in |
| `GET` | `/auth/oidc/callback` | Provider return address |
| `GET` `POST` | `/auth/oidc/complete` | Choose a username after a first provider sign-in (*rate-limited* on `POST`) |
| `GET` | `/auth/shared/start`, `/auth/shared/callback`, `/auth/shared/manage` | Community sign-in, when configured; see [suite](suite.md) |
| `POST` | `/auth/shared/link`, `/auth/shared/unlink` | Connect or disconnect community sign-in |
| `GET` `POST` | `/setup` | First-owner setup. Until an owner exists, other pages redirect here; see [setup](setup.md) |
| `GET` | `/healthz` | Health check; `503` if the database does not answer |
| `GET` | `/static/...`, `/static/comfyware/...` | Stylesheets, scripts and images |
| `GET` | `/branding/{name}` | Custom `mascot` or `favicon` |

## Library

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/gallery` | Your uploads (`q`, `page`, `sort`, `year`, `month`) |
| `POST` | `/gallery/tags` | Add tags to selected files |
| `POST` | `/gallery/album`, `/gallery/visibility` | Add selected files to an album, or change their visibility |
| `GET` | `/trash` | Your Trash |
| `GET` | `/trash/{id}/thumb` | Thumbnail of a trashed file |
| `POST` | `/trash/{id}/restore`, `/trash/{id}/purge` | Restore within 30 days, or delete permanently |
| `GET` | `/favorites` | Your favorites (`q`, `page`) |
| `GET` | `/recent` | Recent uploads across the instance (`page`) |
| `GET` `POST` | `/upload` | Upload page and form |
| `GET` | `/tags` | Tag index |
| `GET` | `/tags/{username}/{slug}` | Files with one account's tag |
| `GET` | `/p/{id}` | Short link; redirects to `/f/{id}` |

## Files

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/f/{id}` | File page |
| `GET` | `/f/{id}/raw` | Original file (supports range requests) |
| `GET` | `/f/{id}/thumb` | Thumbnail or clip poster |
| `GET` | `/f/{id}/preview` | Preview, or the original for animations and clips |
| `GET` | `/f/{id}/rotated` | Rotated full-resolution PNG |
| `GET` | `/f/{id}/map` | Location map, when `IMVAULT_MAP_URL` is set |
| `POST` | `/f/{id}/favorite` | Add (`favorite=1`) or remove (`favorite=0`) a favorite |
| `POST` | `/f/{id}/rotate` | Rotate left, right, or reset |
| `POST` | `/f/{id}/rename` | Rename (owner or administrator) |
| `POST` | `/f/{id}/description` | Set or clear the description (owner or administrator) |
| `POST` | `/f/{id}/visibility` | Set public, members or private |
| `POST` | `/f/{id}/metadata` | Set camera details sharing |
| `POST` | `/f/{id}/location` | Set location sharing |
| `POST` | `/f/{id}/delete` | Move your file to Trash; staff deletion of someone else's file is permanent |
| `POST` | `/f/{id}/tags`, `/f/{id}/tags/{tagID}/delete` | Attach or detach a tag |
| `POST` | `/reports` | Report a file or album |

## Albums

| Method | Path | Description |
| --- | --- | --- |
| `GET` `POST` | `/albums` | Your albums and albums shared with you; create one |
| `GET` | `/a/{slug}` | Album page |
| `POST` | `/a/{slug}/settings` | Rename, or change visibility and access (owner) |
| `POST` | `/a/{slug}/delete` | Delete an album, keeping its files |
| `POST` | `/a/{slug}/files`, `/a/{slug}/files/{fileID}/delete` | Add or remove files |
| `POST` | `/a/{slug}/share` | Prepare a Witmoot draft with the album link |
| `POST` | `/a/{slug}/discussion` | Save or remove a discussion link (owner) |
| `GET` | `/a/{slug}/discussion-return` | Return from Witmoot with a new discussion (owner) |

## Account

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/settings/account` | Account summary, timezone, export and deletion |
| `POST` | `/settings/account/timezone` | Save your display timezone |
| `GET` | `/settings/account/export` | Download your export |
| `POST` | `/settings/account/delete` | Delete your account (*rate-limited*) |
| `POST` | `/settings/account/identities/{id}/delete` | Disconnect a provider |
| `POST` | `/settings/reauth` | Confirm with the provider, for accounts without a password |
| `GET` `POST` | `/settings/profile` | Edit your profile |
| `POST` | `/settings/avatar` | Upload or remove your avatar |
| `POST` | `/settings/avatar-preference` | Show animated or still avatars |
| `GET` | `/members/{id}` | Member profile (signed in) |
| `GET` | `/avatars/{id}` | Avatar image (signed in) |
| `GET` `POST` | `/settings/password` | Change your password (*rate-limited* on `POST`) |
| `POST` | `/settings/email` | Set or clear your email address (*rate-limited*) |
| `GET` | `/settings/2fa` | Two-factor status and setup |
| `GET` | `/settings/2fa/qr` | Setup QR code |
| `POST` | `/settings/2fa/begin`, `/settings/2fa/confirm` | Start and finish setup |
| `POST` | `/settings/2fa/disable`, `/settings/2fa/recovery` | Turn off, or issue new recovery codes (*rate-limited*) |
| `GET` `POST` | `/settings/api-keys` | List and create API keys |
| `POST` | `/settings/api-keys/{id}/delete` | Revoke a key |
| `GET` `POST` | `/invites` | Your invitations, if you may invite (up to 25 uses and 30 days) |
| `POST` | `/invites/{id}/revoke` | Revoke one of your invitations |

## Moderation

Moderators and administrators.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/moderation` | Report queue |
| `GET` | `/moderation/log` | Moderation log |
| `POST` | `/moderation/reports/{id}/resolve` | Remove reported content, or dismiss the report |
| `GET` | `/admin/files` | Every upload |
| `POST` | `/admin/files/{id}/delete` | Delete any file |

## Administration

Administrators only.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/admin` | Overview |
| `GET` | `/admin/metrics` | Site usage and health dashboard |
| `GET` | `/admin/users` | Accounts and usage |
| `POST` | `/admin/users/{id}/limits` | Set storage quota and file size limit |
| `POST` | `/admin/users/{id}/role` | Set member, moderator or administrator |
| `POST` | `/admin/users/{id}/invites` | Allow or remove invitation access |
| `POST` | `/admin/users/{id}/disabled` | Disable or enable an account |
| `POST` | `/admin/users/{id}/delete` | Delete an account and its files |
| `POST` | `/admin/users/{id}/reset` | Issue a one-time password reset link |
| `POST` | `/admin/users/{id}/2fa` | Clear a second factor |
| `POST` | `/admin/users/{id}/avatar/remove` | Remove a member's avatar |
| `GET` `POST` | `/admin/invites` | List (`page`) and create invitations |
| `POST` | `/admin/invites/{id}/revoke` | Revoke an invitation |
| `GET` | `/admin/mail` | Outbound mail queue |
| `POST` | `/admin/mail/{id}/retry`, `/admin/mail/{id}/delete` | Retry or discard a message |
| `GET` `POST` | `/admin/settings` | Instance settings |
| `POST` | `/admin/settings/branding-assets` | Upload or remove the mascot and favicon |
| `POST` | `/admin/settings/clear` | Drop saved settings and use the configuration again |
| `POST` | `/admin/maintenance/storage` | Recalculate storage usage |
| `POST` | `/admin/maintenance/blobs` | Recalculate shared-content reference counts |
