// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"imvault/internal/sandbox"
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
//
// Only picture and sound are carried across. A drone writes its position into
// a subtitle track once a frame, an action camera into a data track, and cover
// art is a still image with Exif of its own; "0:V" is video that is not an
// attached picture.
func (f *FFmpeg) Scrub(ctx context.Context, src, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	args := []string{
		"-v", "error", "-nostdin",
		"-protocol_whitelist", "file,pipe",
		"-i", src,
		"-map", "0:V",
		"-map", "0:a?",
		"-sn", "-dn",
		"-c", "copy",
		"-map_metadata", "-1",
		"-map_metadata:s", "-1",
		"-map_chapters", "-1",
		"-y",
		dst,
	}
	cmd, finish, err := f.command(ctx, f.ffmpegPath, src, dst, args)
	if err != nil {
		return err
	}
	defer finish(false)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := sandbox.RunChild(cmd); err != nil {
		return fmt.Errorf("scrub clip: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return finish(true)
}

// ffmpegTimeout bounds a single ffmpeg or ffprobe invocation.
const ffmpegTimeout = 30 * time.Second

// FFmpeg implements VideoTool using the ffmpeg and ffprobe binaries.
type FFmpeg struct {
	ffmpegPath  string
	ffprobePath string
	bwrapPath   string
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

	args := []string{
		"-v", "error",
		"-protocol_whitelist", "file,pipe",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,duration:stream_tags=DURATION",
		"-show_entries", "format=duration",
		"-of", "json",
		path,
	}
	cmd, finish, err := f.command(ctx, f.ffprobePath, path, "", args)
	if err != nil {
		return VideoInfo{}, err
	}
	defer finish(false)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := sandbox.RunChild(cmd); err != nil {
		return VideoInfo{}, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseProbe(stdout.Bytes())
}

// parseProbe reads ffprobe's JSON. The container's duration is preferred; a
// fragmented MP4 or a Matroska file written without one still usually records
// it on the stream, either as a field or, in Matroska, as a DURATION tag.
// Zero means nobody recorded a duration at all.
func parseProbe(output []byte) (VideoInfo, error) {
	var parsed struct {
		Streams []struct {
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Duration string `json:"duration"`
			Tags     struct {
				Duration string `json:"DURATION"`
			} `json:"tags"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output, &parsed); err != nil {
		return VideoInfo{}, fmt.Errorf("parse ffprobe output: %w", err)
	}
	if len(parsed.Streams) == 0 {
		return VideoInfo{}, fmt.Errorf("the file has no video stream")
	}

	stream := parsed.Streams[0]
	info := VideoInfo{Width: stream.Width, Height: stream.Height}
	for _, candidate := range []int64{
		secondsToMS(parsed.Format.Duration),
		secondsToMS(stream.Duration),
		clockToMS(stream.Tags.Duration),
	} {
		if candidate > 0 {
			info.DurationMS = candidate
			break
		}
	}
	return info, nil
}

// secondsToMS reads ffprobe's decimal seconds, which may be absent or "N/A".
func secondsToMS(raw string) int64 {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || !(seconds > 0) {
		return 0
	}
	if seconds >= math.MaxInt64/1000 {
		return math.MaxInt64 // absurd, and certainly over any limit
	}
	return int64(seconds * 1000)
}

// clockToMS reads a Matroska "HH:MM:SS.fraction" duration tag.
func clockToMS(raw string) int64 {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 3 {
		return 0
	}
	hours, errH := strconv.ParseUint(parts[0], 10, 32)
	minutes, errM := strconv.ParseUint(parts[1], 10, 32)
	seconds, errS := strconv.ParseFloat(parts[2], 64)
	if errH != nil || errM != nil || errS != nil || minutes >= 60 || !(seconds >= 0 && seconds < 60) {
		return 0
	}
	return int64(hours)*3_600_000 + int64(minutes)*60_000 + int64(seconds*1000) // hours < 2^32 cannot overflow
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
		"-protocol_whitelist", "file,pipe",
		"-i", path,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale='min(%d,iw)':-2", max),
		"-f", "image2pipe",
		"-vcodec", "png",
		"pipe:1",
	)

	cmd, finish, err := f.command(ctx, f.ffmpegPath, path, "", args)
	if err != nil {
		return nil, err
	}
	defer finish(false)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := sandbox.RunChild(cmd); err != nil {
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
