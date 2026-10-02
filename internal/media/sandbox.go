// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"imvault/internal/closer"
	"imvault/internal/sandbox"
)

// NewSandboxedFFmpeg requires working tools and namespaces. Requested
// confinement never degrades into unsandboxed processing or missing-tool mode.
func NewSandboxedFFmpeg(ctx context.Context, ffmpeg, ffprobe, bwrap string) (*FFmpeg, error) {
	f := NewFFmpeg(ffmpeg, ffprobe)
	if !f.Available() {
		return nil, fmt.Errorf("media sandbox requires both ffmpeg and ffprobe")
	}
	binary, err := sandbox.Binary(bwrap)
	if err != nil {
		return nil, fmt.Errorf("media sandbox requires Bubblewrap: %w", err)
	}
	f.bwrapPath = binary
	args, err := sandbox.Base(false)
	if err != nil {
		return nil, err
	}
	args = append(args, "--disable-userns")
	if err := sandbox.Check(ctx, binary, args, sandbox.RuntimeEnv()); err != nil {
		return nil, err
	}
	for _, tool := range []string{ffmpeg, ffprobe} {
		if err := checkSandboxTool(ctx, binary, tool); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func checkSandboxTool(ctx context.Context, bwrap, path string) error {
	tool, err := exec.LookPath(path)
	if err != nil {
		return err
	}
	tool, err = filepath.Abs(tool)
	if err != nil {
		return err
	}
	args, err := sandbox.Base(false)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bwrap, append(args, "--disable-userns", "--ro-bind", tool, "/app/tool", "--", "/app/tool", "-version")...)
	cmd.Env = sandbox.RuntimeEnv()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := sandbox.RunChild(cmd); err != nil {
		return fmt.Errorf("sandboxed media tool %s is unavailable: %w: %s", path, err, out.String())
	}
	return nil
}

// command maps exactly one input file and a private job directory into the
// sandbox. The tool cannot see the server's environment or storage directory.
func (f *FFmpeg) command(ctx context.Context, binary, input, output string, args []string) (*exec.Cmd, func(bool) error, error) {
	if f.bwrapPath == "" {
		return exec.CommandContext(ctx, binary, args...), func(bool) error { return nil }, nil
	}
	tool, err := exec.LookPath(binary)
	if err != nil {
		return nil, nil, err
	}
	tool, err = filepath.Abs(tool)
	if err != nil {
		return nil, nil, err
	}
	input, err = filepath.Abs(input)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(input)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("media sandbox input must be a regular file")
	}
	job, err := os.MkdirTemp("", "imvault-media-*")
	if err != nil {
		return nil, nil, err
	}
	mounts, err := sandbox.Base(false)
	if err != nil {
		return nil, nil, errors.Join(err, os.RemoveAll(job))
	}
	insideInput := "/input/media" + filepath.Ext(input)
	insideOutput := "/output/result" + filepath.Ext(output)
	mounts = append(mounts, "--disable-userns", "--ro-bind", tool, "/app/tool", "--ro-bind", input, insideInput, "--bind", job, "/output", "--chdir", "/output")
	mapped := mapMediaArguments(args, input, output, insideInput, insideOutput)
	cmd := exec.CommandContext(ctx, f.bwrapPath, append(mounts, append([]string{"--", "/app/tool"}, mapped...)...)...)
	cmd.Env = sandbox.RuntimeEnv()
	finish := func(success bool) error {
		var err error
		if success && output != "" && ctx.Err() == nil {
			err = copySandboxOutput(filepath.Join(job, filepath.Base(insideOutput)), output)
		}
		return errors.Join(err, os.RemoveAll(job))
	}
	return cmd, finish, nil
}

// abandon is deferred by every media operation to clean up after a failure.
// Its only work is removing the job's scratch directory, and the operation's
// own error is the one worth reporting, so a failure here is dropped.
func abandon(finish func(bool) error) {
	_ = finish(false)
}

func copySandboxOutput(src, dst string) error {
	// A compromised tool may create a symlink pointing outside its sandbox.
	// Refuse it before opening any host path after the subprocess has exited.
	info, err := os.Lstat(src)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("sandbox output must be a regular file")
	}
	input, err := openSandboxOutput(src)
	if err != nil {
		return err
	}
	defer closer.Discard(input)
	output, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(output, input)
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	return err
}

// ConfiguredVideo applies validated application settings.
func ConfiguredVideo(ctx context.Context, ffmpeg, ffprobe string, enabled bool, bwrap string) (*FFmpeg, error) {
	if enabled {
		return NewSandboxedFFmpeg(ctx, ffmpeg, ffprobe, bwrap)
	}
	return NewFFmpeg(ffmpeg, ffprobe), nil
}

func mapMediaArguments(args []string, input, output, insideInput, insideOutput string) []string {
	mapped := append([]string{}, args...)
	for i, arg := range mapped {
		// Probe takes its input last; FFmpeg takes it immediately after -i.
		isInput := (i > 0 && args[i-1] == "-i") || (output == "" && i == len(args)-1)
		if isInput {
			absolute, err := filepath.Abs(arg)
			if err == nil && absolute == input {
				mapped[i] = insideInput
			}
		}
		if output != "" && i == len(args)-1 && arg == output {
			mapped[i] = insideOutput
		}
	}
	return mapped
}
