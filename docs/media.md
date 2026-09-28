# Media handling

What imvault accepts, and what it does with it.

## Limits

| | |
| --- | --- |
| Files per request | 20 |
| Largest single file | Per-account if set, otherwise the instance defaults below |
| Images and animations | `IMVAULT_MAX_UPLOAD_BYTES`, 32 MiB by default |
| Clips | `IMVAULT_MAX_VIDEO_BYTES`, 128 MiB by default |
| Clip duration | `IMVAULT_MAX_VIDEO_DURATION`, 60 seconds by default, and only enforced when ffprobe is available to measure it |
| Pixel count | 100 megapixels, refused before a pixel buffer is allocated |

Thumbnails are fitted within `IMVAULT_THUMB_MAX` (480px by default) and previews
within `IMVAULT_PREVIEW_MAX` (1600px). Both are bounded on their longest edge,
and an image smaller than the bound is left at its original size.

## Metadata

A photo carries more than pixels: the camera, the date, and often the
coordinates it was taken at. The last of those is a home address in the
majority of phone photos, and it travels with the file wherever the file goes.

Every upload and album has separate **EXIF** and **Location** sharing settings.
EXIF covers camera, date, exposure, attribution, and other non-location fields.
Location covers GPS coordinates and altitude, the map, and its link.

| Setting | EXIF | Location |
| --- | --- | --- |
| Default | **Follow visibility**: public files hide EXIF; members-only and private files show it | **Follow EXIF**: use the EXIF choice |
| **Shown** | Share non-location EXIF | Share GPS |
| **Hidden** | Hide non-location EXIF | Hide GPS |

For example, choose EXIF **Shown** and Location **Hidden** to share camera
settings without coordinates. Choose the reverse to show where a photo was taken
without sharing its camera details. Existing photos and albums start with
Location **Follow EXIF**, preserving their previous GPS sharing behavior.

**Albums can only restrict sharing.** Each category is resolved independently
against the file and every album containing it. An album set to **Shown** cannot
reveal a category hidden by the file or another album. An album's **Follow EXIF**
location choice follows that album's EXIF restriction.

### What is removed

When both categories are hidden, whole metadata segments are dropped.
**The pixels are never re-encoded**.
Orientation is applied to renditions, so a rotated phone photo still looks
right; the file keeps the tag that says so until the metadata goes.

| Format | What is removed |
| --- | --- |
| JPEG | The Exif segment, including its embedded thumbnail, plus XMP, IPTC, Photoshop resources, and comments |
| PNG | `tEXt`, `zTXt`, `iTXt`, `eXIf`, and `tIME` |
| WebP | The `EXIF` and `XMP ` chunks, with the VP8X flags cleared and the RIFF size repaired |
| GIF | Comment, plain text, and application extensions — except the loop block, which is how an animation says what it does |
| BMP | Nothing: it has no metadata to begin with |
| WebM, MP4, MOV | The container's tags, including QuickTime's location atom, by remuxing |

When only one category is shown, JPEG, PNG, and WebP downloads receive rebuilt
EXIF containing the permitted standard fields. GPS is kept separately from
camera, date, exposure, lens, and attribution. XMP, IPTC, maker notes, embedded
thumbnails, and unknown fields are removed in either mixed mode: opaque blocks
can carry another copy of coordinates or other hidden details. GIF and BMP do
not have supported structured EXIF; their existing cleanup rules still apply.

Video downloads currently remove all container metadata when either category
is hidden. Independent retention of video metadata is not supported.

The embedded thumbnail inside a JPEG's Exif is worth naming. It is a second,
usually smaller copy of the image, and it is frequently left un-stripped by
implementations that remove the Exif they can see — which is how a file ends up
"cleaned" and still carrying its location.

Clips are remuxed with `ffmpeg -c copy` rather than rewritten by hand. Removing
an atom shifts every absolute sample offset after it, so the container has to be
rebuilt rather than edited, and no re-encoding is involved: the streams are
copied bit for bit.

### What happens when it cannot be removed

Two cases exist. A clip on an instance without ffmpeg, and any image format not
in the table above. In both, the metadata stays where it is and **the file is
not served** to an audience that asked for it to be hidden. The refusal says
what is wrong and how to proceed.

Refusing rather than serving is deliberate. Serving it would be
indistinguishable from success — the page would look exactly like a clean file —
and the one outcome this feature cannot produce is a false sense of safety. It
is also not a dead end: setting both the file's EXIF and location to **Shown** is an explicit
decision to accept the exposure, and the file is served from then on.

### What the page shows

The details are read at upload and stored against the content. After a parser
upgrade, opening an older photo page or fetching its API detail refreshes the
cached fields from the stored original once. Identical uploads share that cache.

Coordinates and the map link appear in a visible **Location** section. Camera
settings remain behind a **collapsed** Photo details disclosure. When no readable
GPS is present, the owner sees an explanation; when privacy settings hide a
location, viewers are told it is hidden. What is shown depends on who is looking:

The owner and administrators always see all extracted details on the photo
page. Other viewers see each category only when its effective setting allows
it. **Hidden EXIF now also hides camera and date on the page**, matching the
setting used for downloads. A separate notice explains when location is hidden.

The authenticated owner API returns all extracted details regardless of sharing
settings. Raw downloads follow the file and album sharing choices even for the
owner; account exports contain the original bytes.

### An optional map

A photograph's page can draw its location as a small static map. This is off
unless the operator sets `IMVAULT_MAP_URL`, a static-map URL template carrying
`{lat}`, `{lon}`, `{zoom}`, and optionally `{key}` placeholders, and supplies
`IMVAULT_MAP_KEY` when the template wants one. Any provider that serves a static
image by coordinates will do.

imvault fetches the image **server-side** and streams it, so the key never
reaches the browser and the content security policy stays `'self'`. The map is
drawn only when the viewer may see the location, following the same policy as
the coordinates themselves: a withheld location has no map, not a blank one. The
coordinates also link to `openstreetmap.org`, which needs no key and works
whether or not a provider is configured.

The trade is explicit: with a map configured, opening a photo whose location you
can see makes one request to the provider, which learns the coordinates and your
server's address. Leave `IMVAULT_MAP_URL` unset and nothing leaves the host.

### Refreshing older photo details

After an EXIF parser fix, refresh details for existing uploads using the same
data directory and database as the service. On a default native install:

```bash
sudo -u imvault env IMVAULT_DATA_DIR=/var/lib/imvault \
  /usr/local/bin/imvault refresh-metadata
```

Pass `IMVAULT_DB` too if the service uses a custom database path. The command
does not read `/etc/conf.d/imvault`. It reads each stored original once, updates
the shared details, and reports how many changed. It can run while the service
is up and is safe to repeat. Unsupported or unreadable metadata leaves existing
details alone; a missing original or database failure exits with an error.
Original bytes, links, and metadata visibility settings are preserved. Re-uploading
an identical photo also refreshes its shared details.

Opening a photo page or fetching `GET /api/v1/files/{id}` now refreshes older
cached details automatically. The command is useful for refreshing the entire
library before browsing. Partial reads preserve previously recovered fields.

### Where filtered copies live

Each filtered copy is stored beside the original, named after the content hash, and built the
first time something needs it. It belongs to the content rather than to a file:
two files with the same bytes share copies, and they are removed when the last
file referring to those bytes goes.

Original and filtered download responses require cache revalidation, so a later
sharing change selects the correct copy. Already downloaded files cannot be recalled.

Originals are never replaced. The account export contains the original, because
the export is the owner's own data going back to the owner.

## De-duplication

Uploads are stored under keys derived from their content hash, so the same bytes
uploaded twice occupy the disk once. A second upload of something already
present reuses the stored original *and* its renditions, skipping the decode and
the resize entirely.

This saves **disk, not quota**: the account really does have one more file, and
is charged for it. Deleting a file removes its bytes only once nothing else
points at them, so one account deleting a shared image cannot take it out from
under another.

Renditions are keyed by content *and* by the settings that produced them, so
changing `IMVAULT_THUMB_MAX` does not leave an existing file handing out a
rendition at the old size. Existing objects keep the keys recorded on their rows
and continue to work; de-duplication applies to uploads from here on.

## What it accepts

Every upload is classified from its leading bytes, not its filename:

| Kind | Detected as | Stored | Grid tile | Detail view |
| --- | --- | --- | --- | --- |
| `image` | anything the image decoder accepts | original + thumb + preview | thumbnail | preview image |
| `animated` | GIF with more than one frame, or WebP with the animation flag | original + thumb | still first frame, swaps to the animation on hover | the animation itself |
| `video` | WebM/Matroska, MP4, MOV | original + poster | poster with a play badge | `<video>` player |

Still images cover JPEG, PNG, WebP, BMP, and TIFF. EXIF orientation is applied,
so a photo taken on a phone is not shown sideways, and transparency is kept:
an image with any non-opaque pixel is encoded as PNG, and an opaque one as JPEG.

Animated assets are never re-encoded: the original bytes are served, so quality
and timing are preserved exactly. Clips are likewise stored untouched — imvault
is a host, not a transcoder.

ffmpeg is used in two narrow ways for clips: `ffprobe` reads dimensions and
duration (so the duration limit can be enforced), and `ffmpeg` extracts one
still frame for the poster. If either binary is missing, imvault logs a warning
at startup and keeps working — clips are accepted on their magic bytes and get a
generated placeholder poster.

`make install` also warns when either tool is missing. On Gentoo, install
`media-video/ffmpeg` and restart imvault so it detects the tools. New uploads then
get video posters. To repair existing placeholder posters, stop the service and
run `imvault rebuild-thumbnails --videos-only` with the service's environment
and OS user. See [rebuilding thumbnails](storage.md#rebuilding-thumbnails-and-posters).
Staged package installs (`DESTDIR=...`) skip the build host's dependency check.

---
