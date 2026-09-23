# Architecture

How the pieces fit together, and why they are arranged that way.

```
cmd/imvault          entrypoint: config, wiring, graceful shutdown
internal/config      environment-driven configuration
internal/logging     the logfmt logger
internal/db          SQLite connection + embedded migrations
internal/models      shared data types
internal/store       all SQL; one file per aggregate
internal/storage     disk and S3 storage behind a cancellable Backend interface
internal/maintenance verified migration, rendition rebuilding, backup/restore
internal/instance    process locks coordinating the server and maintenance
internal/imaging     decode, orient, downscale, encode still images
internal/media       classify uploads; animation and video handling
internal/ratelimit   in-memory token bucket for upload limiting
internal/mail        SMTP delivery, optional and degradable
internal/tokens      opaque secret generation and hashing
internal/apikeys     API key generation, splitting, and verification
internal/web         routing, middleware, handlers, templates, static assets
scripts/demo.sh      boots a seeded throwaway instance (`make demo`)
scripts/genmedia     generates the demo's sample images
contrib/             services, Caddy/nginx/Apache configs, distro packaging
```

A few decisions worth knowing about:

- **Three visibility levels, not two.** Public is the whole internet, members is
  any signed-in account, and private is the owner. A boolean can express "me" and
  "everybody" but not "us", and "us" is the only one of the three a group has any
  use for: without it, signing in grants nothing over being a stranger. The level
  is a setting, so a personal, group, or public host is the same program at
  different points rather than three programs.
- **One visibility predicate.** Every listing, every count, and the tag index go
  through a single `visibilityClause`, so what a listing shows can never diverge
  from what the routes serving the bytes will allow. Retention is folded into the
  same predicate: a lapsed upload disappears the moment it expires, not whenever
  the reaper next runs.
- **A bare semicolon in a form value is percent-encoded before anything parses
  it.** Go's form parser refuses to guess whether a semicolon was meant as a
  separator — it was once accepted as one, and treating it that way was a
  vulnerability — so it reports an error and **drops the whole setting**. A
  report note reading "spam; and there is a lot of it" would arrive empty, a
  search for a filename containing one would match nothing, and a password
  containing one could not be signed in with, which is a lockout for somebody who
  chose it legitimately. Rewriting one into `%3B` says what the caller meant
  rather than losing it, and it is exactly what they should have sent.
- **The audit trail snapshots names rather than joining them.** A moderation
  entry stores the actor's and the target's names as they were when the action
  happened, alongside nullable references to the rows. Deleting the moderator's
  account or the content itself therefore leaves the record intact, which is the
  difference between an audit trail and a list of numbers pointing at nothing.
- **An album decides who may add, never whose files may be added.** An album has
  an `access` level: `owner`, or `members` for one anybody on the instance may
  contribute to. Contributing always means adding your *own* files, so sharing
  an album can never become a way to move somebody else's uploads around, and a
  contributor can take their own file back but not anybody else's. The default
  is the closed level, because an album that quietly accepted anybody's files
  would be a surprise.
- **Tags belong to accounts.** Two people can both use "beach" without sharing a
  label, a count or a lifetime, and a tag is addressed as
  `/tags/{username}/{slug}` because the slug alone is only unique within one
  account. Visibility is still applied when the vocabulary is *exposed*: the tag
  index and its counts are computed over the files you can see, so a tag that
  only labels somebody else's private upload never appears. Tags live with the
  **file's** owner rather than whoever did the tagging, so an administrator
  editing someone else's upload does not leave their own labels on it.
- **SQLite via `modernc.org/sqlite`**, a pure-Go driver, so the binary needs no cgo
  and cross-compiles trivially. The pool is pinned to a single connection, which
  sidesteps `SQLITE_BUSY` entirely and is far beyond this workload's needs.
- **Timestamps are `INTEGER` unix seconds**, so comparisons never depend on driver
  time handling.
- **GIF frame counting walks the block structure** rather than calling
  `gif.DecodeAll`, which would decode every frame into memory just to answer
  "is this animated?".
- **Two template sets.** Pages are parsed as `layout + partials + one page`, and a
  separate set holds the partials alone for htmx swaps. The same `file_grid`
  fragment renders inside a full page and as an htmx response, so markup lives in
  one place.
- **ffmpeg is optional by design.** Anything that depends on it degrades to a
  placeholder and a logged warning rather than a failed upload.
- **File row first, then bytes.** Deletes remove the file record first. The blob
  record remains until object deletion succeeds, so network failures can be
  retried. Per-content locks prevent deletion racing an upload of the same bytes.
- **Content-addressed storage with a trigger-maintained reference count.**
  Identical bytes are stored once however many accounts upload them, and a
  `blobs` table carries a count the database keeps itself rather than one
  application code maintains. The triggers fire for `ON DELETE CASCADE` too, so
  removing an account decrements every count it should; a counter kept by callers
  would leak there, because those rows disappear inside the database.
- **Storage is an interface.** Disk and S3 implement cancellable reads, writes,
  stats, and deletes. S3's seekable reader issues bounded ranges and pins reads
  to an ETag. ffmpeg uses temporary files when storage has no local path.
- **Maintenance requires exclusive access.** The server and ordinary local
  commands share an instance lock; migration, backup, and rendition repair take
  it exclusively. Backups verify the database, media, and encryption key as one
  stopped-instance snapshot. Restores publish a new directory after validation.

## Data layout

```
data/
├── imvault.db
├── secret.key                        # decrypts two-factor secrets
└── objects/
    ├── orig/v1/<ab>/<sha256>.<ext>          # untouched upload
    ├── thumb/v1/<tag>/<ab>/<sha256>.<ext>   # grid thumbnail / clip poster
    └── preview/v1/<tag>/<ab>/<sha256>.<ext> # detail rendition

Objects are addressed by content: `<sha256>` is the hash of the original, `<ab>`
its first two characters as a directory shard, and `<tag>` a short digest of the
thumbnail settings, so a rendition made with different settings is a different
object rather than a stale one served under the same name. Objects stored before
this scheme keep the paths recorded on their rows and still work.
```

File ids are random 12-character base36 strings — unguessable, and private files
are still access-checked rather than relying on that.

---

## Development

```bash
make help          # list every target
make test-js       # JavaScript unit tests (needs node)
make test-browser  # drives a real browser against a throwaway instance
make check        # formatting, vet, Go and JS tests, and complexity
make check-complexity # production Go functions must score 15 or below
make test-race
make build
make demo         # a seeded instance to click around in
```

`make check` checks formatting without rewriting source. Run `make fmt` to
format edits. Complexity is measured with pinned `gocyclo` v0.6.0; its first run
downloads the development tool through Go's module cache. Test functions are
excluded from the complexity ceiling. JavaScript failures propagate to make;
missing optional tools are reported separately.

The test suite covers the image pipeline (including the header-rewind path and
transparency handling), media classification and GIF frame counting, API key
generation and verification, the store (visibility rules, LIKE escaping,
cascades, tag pruning, album slug collisions, key expiry), and the HTTP layer end
to end — registration, multipart upload, renditions, animations, clips with
range requests, tagging, albums, the JSON and plain-text API, and CSRF
enforcement, all against the real templates.

Tests that need ffmpeg or logrotate skip themselves when the tool is unavailable,
so the suite runs anywhere. The same goes for the JavaScript and browser checks:
they skip when node or a Chromium-family browser is missing.

Three layers, each catching what the one below cannot:

| Layer | Catches |
| --- | --- |
| `go test ./...` | Server behaviour, including the exact HTML fragments htmx swaps in |
| `node --test` | The client-side logic: the filter predicate, the dialog's state machine |
| `make test-browser` | That Alpine actually initialises and drives the DOM: the dialog really opens, the filter really hides rows, and no native `confirm()` is used |

That last layer is not ceremony. It caught a row that carried the filter's
`data-search` attribute without the `x-show` binding that applies the predicate,
so the "1 of 4 shown" summary was correct while all four rows stayed on screen —
something no amount of inspecting the markup by eye, or asserting on it over
HTTP, would have shown.

---
