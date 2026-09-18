# Routes

Everything the server answers.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/` | Landing page, or redirect to your gallery |
| `GET` `POST` | `/login`, `/register` | Authentication |
| `POST` | `/logout` | End the session |
| `GET` `POST` | `/forgot` | Request a password reset |
| `GET` `POST` | `/reset/{token}` | Choose a new password |
| `GET` | `/verify/{token}` | Confirm an email address |
| `GET` `POST` | `/settings/password` | Change password, manage email |
| `GET` | `/settings/2fa` | Two-factor status and enrolment |
| `GET` | `/settings/2fa/qr` | Enrolment QR code, while setup is pending |
| `POST` | `/settings/2fa/begin`, `/confirm` | Start and finish enrolment |
| `POST` | `/settings/2fa/disable`, `/recovery` | Turn it off, or reissue recovery codes |
| `GET` `POST` | `/settings/account`, `/settings/account/delete` | Account summary and deletion |
| `GET` `POST` | `/login/2fa` | The second sign-in step |
| `POST` | `/settings/email` | Set or clear the email address |
| `GET` | `/gallery` | Your uploads (supports `?q=` and `?page=`) |
| `GET` | `/recent` | The instance feed: what everybody has shared (supports `?page=`) |
| `GET` `POST` | `/upload` | Uploader and upload endpoint |
| `GET` | `/f/{id}` | Asset detail page |
| `GET` | `/f/{id}/raw` | Original file (range requests supported) |
| `GET` | `/f/{id}/thumb` | Thumbnail or clip poster |
| `GET` | `/f/{id}/preview` | Preview, or the original for animations and clips |
| `POST` | `/f/{id}/visibility` | Set the level: public, members, or private |
| `POST` | `/f/{id}/delete` | Delete the file |
| `POST` | `/f/{id}/tags`, `/f/{id}/tags/{tagID}/delete` | Attach or detach a tag |
| `GET` `POST` | `/albums` | List and create albums; lists the ones others shared too |
| `GET` | `/a/{slug}` | Album page |
| `POST` | `/a/{slug}/settings` | Rename, change visibility or sharing (owner only) |
| `POST` | `/a/{slug}/delete` | Delete an album (keeps its files) |
| `POST` | `/a/{slug}/files`, `/a/{slug}/files/{fileID}/delete` | Add or remove album members |
| `GET` | `/tags` | Tag index |
| `GET` | `/tags/{username}/{slug}` | Files carrying one account's tag |
| `GET` | `/p/{id}` | Short share link (redirects to `/f/{id}`) |
| `GET` `POST` | `/settings/api-keys` | Manage API keys |
| `POST` | `/settings/api-keys/{id}/delete` | Revoke a key |
| `GET` | `/admin` | Admin overview |
| `GET` | `/admin/users` | Account list with usage |
| `POST` | `/admin/users/{id}/quota` | Set an account's storage cap |
| `POST` | `/admin/users/{id}/admin` | Grant or revoke administrator rights |
| `POST` | `/admin/users/{id}/disabled` | Disable or enable an account |
| `POST` | `/admin/users/{id}/delete` | Delete an account and its files |
| `POST` | `/admin/users/{id}/reset` | Issue a one-time password reset link |
| `POST` | `/admin/users/{id}/2fa` | Clear an account's second factor |
| `GET` | `/admin/mail` | Outbound mail queue |
| `POST` | `/admin/mail/{id}/retry` | Requeue a failed message |
| `POST` | `/admin/mail/{id}/delete` | Discard a message |
| `GET` `POST` | `/admin/settings` | Instance-wide policy: signup, anonymous uploads, retention |
| `POST` | `/admin/settings/clear` | Drop the stored overrides and follow the configuration again |
| `POST` | `/admin/maintenance/storage` | Recalculate per-account storage usage |
| `POST` | `/admin/maintenance/blobs` | Recalculate content reference counts |
| `GET` | `/admin/files` | Every upload, for moderation |
| `POST` | `/admin/files/{id}/delete` | Delete any file |
| `GET` | `/healthz` | Liveness probe |

Mutating requests that use cookies must carry the CSRF token, either as the
`X-CSRF-Token` header (htmx does this automatically) or as a `csrf_token` form
field. The `/api/v1/...` endpoints never use cookies and are exempt.

---
