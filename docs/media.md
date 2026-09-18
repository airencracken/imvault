# Media handling

What imvault accepts, and what it does with it.

## Limits

| | |
| --- | --- |
| Files per request | 20 |
| Images and animations | `IMVAULT_MAX_UPLOAD_BYTES`, 32 MiB by default |
| Clips | `IMVAULT_MAX_VIDEO_BYTES`, 128 MiB by default |
| Clip duration | `IMVAULT_MAX_VIDEO_DURATION`, 60 seconds by default, and only enforced when ffprobe is available to measure it |
| Pixel count | 100 megapixels, refused before a pixel buffer is allocated |

Thumbnails are fitted within `IMVAULT_THUMB_MAX` (480px by default) and previews
within `IMVAULT_PREVIEW_MAX` (1600px). Both are bounded on their longest edge,
and an image smaller than the bound is left at its original size.

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
