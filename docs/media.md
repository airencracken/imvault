# Media handling

## Limits

| | |
| --- | --- |
| Files per request | 20 |
| Largest single file | The account's limit if set, otherwise the instance defaults below |
| Images and animations | `IMVAULT_MAX_UPLOAD_BYTES`, default 32 MiB |
| Clips | `IMVAULT_MAX_VIDEO_BYTES`, default 128 MiB |
| Clip duration | `IMVAULT_MAX_VIDEO_DURATION`, default 60 seconds; checked only when ffprobe is available |
| Pixel count | 24 megapixels |

Thumbnails fit within `IMVAULT_THUMB_MAX` (default 480px) and previews within
`IMVAULT_PREVIEW_MAX` (default 1600px), measured on the longest edge. Smaller
images keep their size.

## What it accepts

Imvault identifies each upload by its first bytes, not its filename:

| Kind | Detected as | Stored | Grid tile | Detail view |
| --- | --- | --- | --- | --- |
| `image` | JPEG, PNG, WebP, BMP, TIFF | original, thumbnail, preview | thumbnail | preview |
| `animated` | GIF with more than one frame, or animated WebP | original, thumbnail | first frame; plays on hover | the animation |
| `video` | WebM/Matroska, MP4, MOV | original, poster | poster with a play badge | `<video>` player |

Phone photos are turned upright using their EXIF orientation. Renditions with
any transparency are saved as PNG, opaque ones as JPEG. Animations and clips are
served as uploaded, never re-encoded or transcoded.

For clips, `ffprobe` reads dimensions and duration, and `ffmpeg` grabs one frame
for the poster. Without them, Imvault logs a warning at startup, still accepts
clips, and gives them a placeholder poster. `make install` warns when either
tool is missing, except in staged (`DESTDIR=...`) installs.

To add posters later, install ffmpeg (`media-video/ffmpeg` on Gentoo) and restart
Imvault. New uploads get real posters. For existing clips, stop the service and
run `imvault rebuild-thumbnails --videos-only`; see
[rebuilding thumbnails](storage.md#rebuilding-thumbnails-and-posters).

## Metadata

Photos often carry the camera, the date and the coordinates where they were
taken. On a phone photo, that is often a home address.

Every file has two choices, on its page under **Edit**:

- **Camera details**: camera, date, exposure, attribution and other non-location
  fields.
- **Location**: GPS coordinates, altitude, the map and its link.

Both start hidden on public uploads and shown on members-only and private ones.
Making a file public hides both again; show them afterwards if you want to.
Albums do not change either choice.

### What is removed

When both settings are hidden, Imvault drops whole metadata blocks. Pixels are
never re-encoded.

| Format | What is removed |
| --- | --- |
| JPEG | The Exif segment and its embedded thumbnail, plus XMP, IPTC, Photoshop resources and comments |
| PNG | `tEXt`, `zTXt`, `iTXt`, `eXIf` and `tIME` |
| WebP | The `EXIF` and `XMP ` chunks |
| GIF | Comment, plain-text and application extensions, except the loop block |
| BMP | Nothing; BMP has no metadata |
| WebM, MP4, MOV | Container tags, including QuickTime's location atom, by remuxing with `ffmpeg -c copy` |

When only one setting is shown, JPEG, PNG and WebP downloads get fresh EXIF with
just the allowed standard fields. XMP, IPTC, maker notes, embedded thumbnails and
unknown fields are always removed in this case, because they can hide another
copy of the coordinates. GIF and BMP follow the rules in the table.

Clips lose all container metadata when either setting is hidden.

### What happens when it cannot be removed

On an instance without ffmpeg, clips cannot be cleaned, and neither can formats
missing from the table. Imvault then **withholds the file** from anyone who should
not see its metadata, and the page explains why. To serve it anyway, show both
camera details and location on the file's page.

### What the page shows

Details are read at upload and shared by identical uploads. Older photos refresh
their details from the original the first time their page or API detail is
opened after an upgrade.

Coordinates and the map link sit in a **Location** section. Camera settings sit
in a collapsed **Photo details** section. If a photo has no readable GPS, the
owner sees why. If the location is hidden, viewers are told so.

The owner and administrators always see every detail on the page. Other viewers
see each one only when it is shown. Hidden camera details also hide the camera
and date on the page.

The owner's API returns every detail. Downloads follow the sharing settings,
even for the owner. Account exports contain the untouched originals.

### An optional map

A photo page can show a small static map of its location. Set `IMVAULT_MAP_URL`
to a static-map URL template with `{lat}`, `{lon}`, `{zoom}` and, if needed,
`{key}` placeholders, and set `IMVAULT_MAP_KEY` if the provider needs one.
`IMVAULT_MAP_ZOOM` defaults to 13.

Imvault fetches the map on the server, so the key never reaches the browser. A
viewer who cannot see the location gets no map. The coordinates also link to
`openstreetmap.org`, with or without a provider.

With a map configured, each view of a photo with a visible location sends the
coordinates and your server's address to the provider. Leave `IMVAULT_MAP_URL`
unset and nothing leaves the host.

### Refreshing older photo details

To refresh details for the whole library at once, run `refresh-metadata` with
the service's data directory:

```bash
sudo -u imvault env IMVAULT_DATA_DIR=/var/lib/imvault \
  /usr/bin/imvault refresh-metadata
```

Packages install to `/usr/bin`; `make install` uses `/usr/local/bin`. Add
`IMVAULT_DB` if the service uses a custom database path; the command does not
read `/etc/conf.d/imvault`. It reads each original once and reports how many
changed. It can run while the service is up and is safe to repeat. Unreadable
metadata leaves existing details alone. A missing original or database error
exits nonzero. Originals, links and sharing settings are unchanged.

### Filtered copies

Filtered copies are built the first time they are needed and stored next to the
original. Identical uploads share them, and they go when the last file using
those bytes is deleted. Browsers revalidate downloads, so a sharing change takes
effect at once. Copies already downloaded cannot be recalled.

## De-duplication

Identical uploads are stored once and reuse the existing renditions. This saves
disk, not quota: each upload still counts against its account. Stored bytes are
deleted only when no file uses them, so one person deleting a shared image
cannot remove it from someone else.

Renditions are keyed by content and by the settings that made them, so changing
`IMVAULT_THUMB_MAX` produces new thumbnails instead of serving old sizes.
