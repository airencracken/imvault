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

Every upload has a **metadata setting**, and so does every album:

| Setting | What it does |
| --- | --- |
| **Follow visibility** | The default. Public files hide their metadata; members-only and private files keep it |
| **Shown** | Always keep it, however the file is shared |
| **Hidden** | Never serve it |

Following visibility is the default because the audience usually answers the
question: the group a file was shared with is the people who took the picture,
and the public is not. The explicit settings are there because the default is a
good rule and a poor ceiling — people have opinions about individual pictures.

**An album can only ever tighten.** The setting that applies is the most
restrictive of the file's own, its visibility's default, and every album it is
in. Albums do not change who can see a file, and they must not change what it
reveals either, in either direction: a shared album's owner is often not the
file's owner, and one person must not be able to loosen another's privacy, nor
to make their file more exposed than they chose.

### What is removed

Whole metadata segments are dropped, and **the pixels are never re-encoded**.
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
is also not a dead end: setting the file's metadata to **Shown** is an explicit
decision to accept the exposure, and the file is served from then on.

### Where the metadata-free copy lives

It is stored beside the original, named after the content hash, and built the
first time something needs it. It belongs to the content rather than to a file:
two files with the same bytes share one copy, and it is removed when the last
file referring to those bytes goes.

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
still frame for the poster. If neither binary is present, imvault logs a warning
at startup and keeps working — clips are accepted on their magic bytes and get a
generated placeholder poster.

---
