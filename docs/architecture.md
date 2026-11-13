# Architecture

```
cmd/imvault          entrypoint: config, wiring, graceful shutdown
internal/config      environment-driven configuration
internal/db          SQLite connection and embedded migrations
internal/store       all SQL; one file per aggregate
internal/models      shared data types
internal/storage     disk and S3 backends
internal/maintenance storage migration, rendition rebuilds, backup and restore
internal/instance    locks shared by the server and maintenance commands
internal/imaging     decode, orient, downscale and encode still images
internal/media       classify uploads; animations and video
internal/metadata    strip identifying metadata from served media
internal/accounts    account validation shared by forms and local commands
internal/apikeys     API key generation and verification
internal/invites     invitation codes
internal/oidc        OpenID Connect sign-in
internal/secrets     encryption for stored secrets such as TOTP keys
internal/totp        time-based one-time passwords
internal/web         routes, middleware, handlers, templates, static assets, mail queue
scripts/demo.sh      seeded throwaway instance (make demo)
scripts/genmedia     sample images for the demo
contrib/             services, proxy configs, distro packaging
```

## Design notes

- **Three visibility levels.** Public is everyone, members is any signed-in
  account, private is the owner. Instance profiles are settings over one program.
- **One visibility predicate.** Listings, counts and the tag index all use
  `visibilityClause` in `internal/store`, so a listing never shows what the
  byte-serving routes would refuse. Expired anonymous uploads drop out of it
  immediately.
- **Semicolons in form values are percent-encoded before parsing.** Go's form
  parser otherwise drops the whole value, which would empty a report note or
  lock out a password that contains one.
- **The moderation log stores names as they were.** Deleting an account or the
  content keeps the entry readable.
- **Albums control who may add, never whose files.** `access` is `owner` (the
  default) or `members`. Contributors add only their own files and can remove
  only those.
- **Tags belong to the file's owner.** They are addressed as
  `/tags/{username}/{slug}`. The tag index counts only files the viewer can see.
- **SQLite through `modernc.org/sqlite`.** No cgo. The pool holds one
  connection.
- **Timestamps are `INTEGER` Unix seconds.**
- **Two template sets.** Pages parse as layout, partials and one page; a second
  set holds the partials alone for htmx swaps, so fragments like `file_grid`
  live in one place.
- **ffmpeg is optional.** Without it, uploads get a placeholder and a logged
  warning.
- **Deletes remove the file row first.** The blob row stays until the object is
  gone, so storage failures can be retried. Per-content locks stop a delete
  racing an upload of the same bytes.
- **Storage is content-addressed.** Identical bytes are stored once. Database
  triggers keep the `blobs` reference count, including on `ON DELETE CASCADE`.
- **S3 reads are ranged and pinned to an ETag.** ffmpeg gets a temporary file
  when storage has no local path.
- **Maintenance takes locks.** Storage migration and rendition repair need
  exclusive access. Backups take a shared lock and a SQLite snapshot, so
  browsing and uploads continue. Restores publish a new directory after
  validation.

## Data layout

```
data/
├── imvault.db
├── secret.key                               # decrypts two-factor secrets
└── objects/
    ├── orig/v1/<ab>/<sha256>.<ext>          # untouched upload
    ├── thumb/v1/<tag>/<ab>/<sha256>.<ext>   # grid thumbnail or clip poster
    └── preview/v1/<tag>/<ab>/<sha256>.<ext> # detail rendition
```

`<sha256>` is the hash of the original and `<ab>` its first two characters.
`<tag>` is a digest of the rendition settings, so changed settings produce new
objects. Older objects keep the paths recorded on their rows.

File IDs are random 12-character base36 strings. Private files are still
access-checked.

## Development

```bash
make help              # every target
make build
make demo              # seeded instance to click around in
make fmt               # format Go source in place
make check             # config, CLI and format checks, vet, Go and JS tests, complexity, release check
make test-race
make test-browser      # drives Chromium against a throwaway instance (needs node)
```

`make check` does not rewrite files; run `make fmt` first. The complexity
ceiling is 15 for non-test Go functions, measured with `gocyclo` v0.6.0. Tests
that need ffmpeg, logrotate, node or a Chromium-family browser skip when the
tool is missing.

Shared code comes from published Comfylib modules pinned in `go.mod`. Shared
browser assets are served from `ui.Assets()` at `/static/comfyware/`; Comfylib's
`ui` styles load after the app stylesheet. To work against a local Comfylib
checkout, use an untracked `go.work`:

```sh
go work init . ../comfylib
```

Release checks run with `GOWORK=off`. Do not commit `go.work` or `replace`
directives. `python3 scripts/comfy.py check` runs the shared configuration,
release and container checks from the pinned Comfylib version.
