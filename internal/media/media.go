// SPDX-License-Identifier: AGPL-3.0-or-later

// Package media classifies an upload and produces the renditions the UI needs.
//
// It sits above package imaging: still images are handed straight to the image
// pipeline, animated GIFs and WebPs keep their original bytes (the browser
// plays them) and get a still first-frame thumbnail, and video containers are
// probed and poster-framed with ffmpeg when it is available.
package media

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/gif"
	"io"
	"os"
	"time"

	"imvault/internal/imaging"
	"imvault/internal/models"

	// Registered so poster frames decoded from ffmpeg's PNG output work.
	_ "image/png"
)

// Processor builds renditions for uploads.
type Processor struct {
	ThumbMax         int
	PreviewMax       int
	JPEGQuality      int
	MaxVideoDuration time.Duration

	// Video is the clip tooling. It may be nil or report Available() == false,
	// in which case clips are still accepted but get a placeholder poster and
	// no duration check.
	Video VideoTool
}

// NewProcessor returns a Processor configured from application settings.
func NewProcessor(thumbMax, previewMax, quality int, maxVideoDuration time.Duration, video VideoTool) *Processor {
	return &Processor{
		ThumbMax:         thumbMax,
		PreviewMax:       previewMax,
		JPEGQuality:      quality,
		MaxVideoDuration: maxVideoDuration,
		Video:            video,
	}
}

// VideoEnabled reports whether clip probing and poster extraction are active.
func (p *Processor) VideoEnabled() bool {
	return p.Video != nil && p.Video.Available()
}

// Result describes a processed upload.
type Result struct {
	Kind   models.Kind
	Mime   string
	Ext    string
	Width  int
	Height int

	// Thumb is always populated. It is what grids display.
	Thumb *imaging.Rendition

	// Preview is nil for animations and clips: their original bytes are the
	// thing the browser should play, so the preview endpoint falls back to the
	// stored object.
	Preview *imaging.Rendition

	DurationMS int64
	FrameCount int

	// Warnings records non-fatal problems, such as a poster frame that could
	// not be extracted. The caller is expected to log them.
	Warnings []string
}

// ProcessStill handles every non-video upload.
//
// src must be seekable and positioned at the start; format comes from Classify.
func (p *Processor) ProcessStill(src io.ReadSeeker, format Format) (*Result, error) {
	switch format {
	case FormatGIF:
		return p.processGIF(src)
	case FormatAnimatedWebP:
		return p.processAnimatedWebP(src)
	default:
		return p.processImage(src)
	}
}

// processImage runs the ordinary still-image pipeline.
func (p *Processor) processImage(src io.ReadSeeker) (*Result, error) {
	res, err := imaging.Process(src, imaging.Options{
		ThumbMax:    p.ThumbMax,
		PreviewMax:  p.PreviewMax,
		JPEGQuality: p.JPEGQuality,
	})
	if err != nil {
		return nil, err
	}

	return &Result{
		Kind:    models.KindImage,
		Mime:    res.Mime,
		Ext:     res.Ext,
		Width:   res.Width,
		Height:  res.Height,
		Thumb:   &res.Thumb,
		Preview: &res.Preview,
	}, nil
}

// processGIF keeps multi-frame GIFs intact and thumbnails the first frame.
func (p *Processor) processGIF(src io.ReadSeeker) (*Result, error) {
	frames, err := gifFrameCount(src)
	if err != nil || frames <= 1 {
		// A single-frame GIF is just a still image.
		return p.processImage(src)
	}

	if err := rewind(src); err != nil {
		return nil, err
	}
	first, err := gif.Decode(src)
	if err != nil {
		return nil, fmt.Errorf("decode gif: %w", err)
	}

	thumb, err := imaging.Render(first, p.ThumbMax, p.JPEGQuality)
	if err != nil {
		return nil, fmt.Errorf("render thumbnail: %w", err)
	}

	b := first.Bounds()
	return &Result{
		Kind:       models.KindAnimated,
		Mime:       "image/gif",
		Ext:        "gif",
		Width:      b.Dx(),
		Height:     b.Dy(),
		Thumb:      &thumb,
		FrameCount: frames,
	}, nil
}

// processAnimatedWebP thumbnails the first frame of an animated WebP.
func (p *Processor) processAnimatedWebP(src io.ReadSeeker) (*Result, error) {
	res := &Result{Kind: models.KindAnimated, Mime: "image/webp", Ext: "webp"}

	if rendered, err := imaging.Process(src, imaging.Options{
		ThumbMax:    p.ThumbMax,
		PreviewMax:  p.PreviewMax,
		JPEGQuality: p.JPEGQuality,
	}); err == nil {
		res.Width, res.Height = rendered.Width, rendered.Height
		res.Thumb = &rendered.Thumb
		return res, nil
	}

	// The first frame could not be decoded; still accept the file.
	placeholder := placeholderThumb(p.ThumbMax)
	res.Thumb = &placeholder
	res.Warnings = append(res.Warnings, "could not decode a poster frame for this animation")
	return res, nil
}

// ProcessVideo probes a clip and extracts a poster frame.
//
// path is a filesystem path to the same bytes as src and may be empty, in which
// case a temporary copy is made because ffmpeg needs a seekable file.
func (p *Processor) ProcessVideo(ctx context.Context, src io.ReadSeeker, path string, format Format) (*Result, error) {
	res := &Result{Kind: models.KindVideo, Mime: format.Mime(), Ext: format.Ext()}

	if !p.VideoEnabled() {
		placeholder := placeholderThumb(p.ThumbMax)
		res.Thumb = &placeholder
		res.Warnings = append(res.Warnings,
			"ffmpeg was not found, so this clip has a placeholder poster and no duration check")
		return res, nil
	}

	file, cleanup, err := materialise(src, path)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	info, err := p.Video.Probe(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("could not read the clip: %w", err)
	}
	res.Width, res.Height, res.DurationMS = info.Width, info.Height, info.DurationMS

	if p.MaxVideoDuration > 0 && res.DurationMS > 0 &&
		res.DurationMS > p.MaxVideoDuration.Milliseconds() {
		return nil, fmt.Errorf("clip runs %s, the limit is %s",
			formatDuration(res.DurationMS), p.MaxVideoDuration)
	}

	// Skip a possible black opening frame on anything long enough to have one.
	var at time.Duration
	if res.DurationMS > 2000 {
		at = time.Second
	}

	raw, err := p.Video.Poster(ctx, file, at, p.ThumbMax)
	if err != nil {
		res.Warnings = append(res.Warnings, "poster frame extraction failed: "+err.Error())
	} else if thumb := renditionFromImage(raw, p.ThumbMax, p.JPEGQuality); thumb != nil {
		res.Thumb = thumb
	} else {
		res.Warnings = append(res.Warnings, "poster frame could not be decoded")
	}

	if res.Thumb == nil {
		placeholder := placeholderThumb(p.ThumbMax)
		res.Thumb = &placeholder
	}
	return res, nil
}

// renditionFromImage decodes encoded bytes (ffmpeg's PNG output) and re-renders
// them through the shared image pipeline so clip posters match image thumbs.
func renditionFromImage(raw []byte, max, quality int) *imaging.Rendition {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	r, err := imaging.Render(img, max, quality)
	if err != nil {
		return nil
	}
	return &r
}

// materialise returns a filesystem path for src, creating a temporary copy when
// path is empty. The returned cleanup is always safe to call.
func materialise(src io.ReadSeeker, path string) (string, func(), error) {
	if path != "" {
		return path, func() {}, nil
	}

	tmp, err := os.CreateTemp("", "imvault-clip-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp file: %w", err)
	}
	name := tmp.Name()
	cleanup := func() { os.Remove(name) }

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("rewind clip: %w", err)
	}
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("copy clip: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close temp file: %w", err)
	}
	return name, cleanup, nil
}

// rewind returns a seekable reader to its start.
func rewind(rs io.ReadSeeker) error {
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind: %w", err)
	}
	return nil
}

func formatDuration(ms int64) string {
	d := (ms + 500) / 1000
	if d < 60 {
		return fmt.Sprintf("%ds", d)
	}
	return fmt.Sprintf("%dm%02ds", d/60, d%60)
}
