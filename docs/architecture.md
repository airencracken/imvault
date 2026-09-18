# Architecture

How the pieces fit together, and why they are arranged that way.

```
cmd/imvault          entrypoint: config, wiring, graceful shutdown
internal/config      environment-driven configuration
internal/logging     the logfmt logger
internal/db          SQLite connection + embedded migrations
internal/models      shared data types
internal/store       all SQL; one file per aggregate
internal/storage     object storage behind a Backend interface (disk today)
internal/imaging     decode, orient, downscale, encode still images
internal/media       classify uploads; animation and video handling
internal/ratelimit   in-memory token bucket for upload limiting
internal/mail        SMTP delivery, optional and degradable
internal/tokens      opaque secret generation and hashing
internal/apikeys     API key generation, splitting, and verification
internal/web         routing, middleware, handlers, templates, static assets
scripts/demo.sh      boots a seeded throwaway instance (`make demo`)
scripts/genmedia     generates the demo's sample images
contrib/             systemd unit, OpenRC script, Alpine and Gentoo packaging
```

A few decisions worth knowing about:

- **One visibility predicate.** Every listing, every count, and the tag index go
  through a single `visibilityClause`, so what a listing shows can never diverge
  from what the routes serving the bytes will allow. Retention is folded into the
  same predicate: a lapsed upload disappears the moment it expires, not whenever
  the reaper next runs.
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
- **Row first, then bytes.** Deletes remove the database row before the objects, so
  a crash leaves harmless orphaned files rather than dangling references.
- **Storage is an interface.** Swapping the disk backend for S3 means implementing
  `storage.Backend`; no handler changes.

## Data layout

```
data/
├── imvault.db
└── objects/
    ├── orig/YYYY/MM/<id>.<ext>       # untouched upload
    ├── thumb/YYYY/MM/<id>.<ext>      # grid thumbnail / clip poster
    └── preview/YYYY/MM/<id>.<ext>    # detail rendition (still images only)
```

File ids are random 12-character base36 strings — unguessable, and private files
are still access-checked rather than relying on that.

---

## Development

```bash
make help          # list every target
make test-js       # JavaScript unit tests (needs node)
make test-browser  # drives a real browser against a throwaway instance
make check        # gofmt + go vet + go test, which is what CI should run
make test-race
make build
make demo         # a seeded instance to click around in
```

The test suite covers the image pipeline (including the header-rewind path and
transparency handling), media classification and GIF frame counting, API key
generation and verification, the store (visibility rules, LIKE escaping,
cascades, tag pruning, album slug collisions, key expiry), and the HTTP layer end
to end — registration, multipart upload, renditions, animations, clips with
range requests, tagging, albums, the JSON and plain-text API, and CSRF
enforcement, all against the real templates.

Tests that need ffmpeg skip themselves when it is unavailable, so the suite runs
anywhere. The same goes for the JavaScript and browser checks: they skip when
node or a Chromium-family browser is missing.

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
