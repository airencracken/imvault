# README images

`recent.png` is a screenshot of the actual local demo with generated test media.
To refresh it, start `make demo`, then in another terminal run:

```bash
node scripts/screenshot.mjs http://127.0.0.1:8080 docs/images/recent.png
```

The capture uses an isolated temporary Chrome profile and the demo login. It
only accepts loopback URLs. Node and a Chromium-family browser are required;
`CHROME` can name a browser binary. `SCREENSHOT_USERNAME` and
`SCREENSHOT_PASSWORD` can override the demo credentials.

`mascot.png` is the original transparent mascot generated with the built-in
imagegen tool. The app uses a 256-pixel copy plus 16- and 32-pixel favicon copies
under `internal/web/static/img/`, with transparency preserved.

Generation prompt:

> Use case: logo-brand. Asset type: a single mascot logo for imvault, a small self-hosted image and short clip library; must also work as its favicon. Primary request: a cute little mascot, an original friendly rounded blue vault creature with two small feet, tiny dot eyes and a simple smile; its body is a rounded square photo frame, with a very simple mountain-and-sun picture on its belly, suggesting a safe home for pictures. Style: crisp flat illustration with bold, clean, rounded outlines and minimal detail, warm and approachable, distinctive silhouette, designed to stay legible at 32 pixels. Composition: one centered character filling most of a square canvas, generous enough padding to avoid clipping, front view. Color palette matching the existing app: periwinkle blue #6ea8fe, deep navy #101216 outlines, pale cream highlights. Background: genuinely transparent alpha, no colored backdrop, no ground shadow. No text, letters, typography, extra objects, gradients, tiny decorations, 3D rendering, mockup, borders, or watermark. Produce one finished square transparent PNG asset.
