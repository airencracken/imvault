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
