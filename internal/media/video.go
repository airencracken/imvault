// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// VideoInfo is what probing a clip tells us.
type VideoInfo struct {
	Width      int
	Height     int
	DurationMS int64
}

// VideoTool probes clips and extracts poster frames.
type VideoTool interface {
	// Available reports whether the tooling can actually run.
	Available() bool
	// Probe returns the first video stream's dimensions and the container
	// duration.
	Probe(ctx context.Context, path string) (VideoInfo, error)
	// Poster renders a single frame at the given offset as PNG bytes, scaled
	// so its longest edge is at most max. A zero offset means "first frame".
	Poster(ctx context.Context, path string, at time.Duration, max int) ([]byte, error)
	// Scrub writes a copy of a clip at dst with its container metadata removed.
	Scrub(ctx context.Context, src, dst string) error
}

// Scrub remuxes a clip without its metadata.
//
// It copies the streams rather than re-encoding them, so the picture is bit for
// bit what it was and only the container is rebuilt. That is the part that
// makes this worth shelling out for: a metadata atom sits in the middle of the
// file, and removing it shifts every absolute sample offset that follows, so a
// hand-written rewrite would have to find and repair all of them. ffmpeg
// already knows how.
//
// -map_metadata -1 clears the container's own tags, including the QuickTime
// location atom an iPhone writes, and -map_chapters -1 drops chapter titles
// that can name places. Stream-level tags are cleared too, since a title is as
// identifying as a comment.
func (f *FFmpeg) Scrub(ctx context.Context, src, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, f.ffmpegPath,
		"-v", "error",
		"-i", src,
		"-map", "0",
		"-c", "copy",
		"-map_metadata", "-1",
		"-map_metadata:s", "-1",
		"-map_chapters", "-1",
		"-y",
		dst,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("scrub clip: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ffmpegTimeout bounds a single ffmpeg or ffprobe invocation.
const ffmpegTimeout = 30 * time.Second

// FFmpeg implements VideoTool using the ffmpeg and ffprobe binaries.
type FFmpeg struct {
	ffmpegPath  string
	ffprobePath string
	timeout     time.Duration
	available   bool
}

// NewFFmpeg returns an FFmpeg tool, recording up front whether both binaries can
// be found. A missing binary makes the tool report Available() == false rather
// than failing at request time.
func NewFFmpeg(ffmpegPath, ffprobePath string) *FFmpeg {
	return &FFmpeg{
		ffmpegPath:  ffmpegPath,
		ffprobePath: ffprobePath,
		timeout:     ffmpegTimeout,
		available:   binaryAvailable(ffmpegPath) && binaryAvailable(ffprobePath),
	}
}

// Available reports whether both binaries were found.
func (f *FFmpeg) Available() bool { return f.available }

// Probe reads stream dimensions and container duration.
func (f *FFmpeg) Probe(ctx context.Context, path string) (VideoInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, f.ffprobePath,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-show_entries", "format=duration",
		"-of", "json",
		path,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return VideoInfo{}, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	var parsed struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		return VideoInfo{}, fmt.Errorf("parse ffprobe output: %w", err)
	}
	if len(parsed.Streams) == 0 {
		return VideoInfo{}, fmt.Errorf("the file has no video stream")
	}

	info := VideoInfo{
		Width:  parsed.Streams[0].Width,
		Height: parsed.Streams[0].Height,
	}
	// Duration is reported as a string and may be absent or "N/A".
	if seconds, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil && seconds > 0 {
		info.DurationMS = int64(seconds * 1000)
	}
	return info, nil
}

// Poster extracts one frame and returns it as PNG bytes.
func (f *FFmpeg) Poster(ctx context.Context, path string, at time.Duration, max int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	args := []string{"-v", "error", "-nostdin", "-an"}
	if at > 0 {
		// Seeking before -i lets ffmpeg jump rather than decode from the start.
		args = append(args, "-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64))
	}
	args = append(args,
		"-i", path,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale='min(%d,iw)':-2", max),
		"-f", "image2pipe",
		"-vcodec", "png",
		"pipe:1",
	)

	cmd := exec.CommandContext(ctx, f.ffmpegPath, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg produced no frame")
	}
	return stdout.Bytes(), nil
}

// binaryAvailable resolves a configured path, accepting both a bare command
// name to look up on PATH and an explicit filesystem path.
func binaryAvailable(path string) bool {
	if path == "" {
		return false
	}
	if strings.ContainsRune(path, filepath.Separator) {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	_, err := exec.LookPath(path)
	return err == nil
}
