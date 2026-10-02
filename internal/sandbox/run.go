// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"imvault/internal/closer"
)

// Relay output through the supervisor. Journald can then attribute records to
// the service's main process even when the server lives in another PID namespace.
type serviceLog struct{ io.Writer }

// Run supervises Bubblewrap and forwards shutdown to the actual server.
// Bubblewrap's outer monitor does not forward SIGTERM itself. JSON status gives
// us the server's host PID; --as-pid-1 avoids signalling an intermediate reaper.
func Run(ctx context.Context, binary string, args, env []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer closer.Discard(reader)
	options := []string{"--as-pid-1", "--die-with-parent", "--json-status-fd", "3"}
	cmd := exec.Command(binary, append(options, args...)...)
	cmd.Env = env
	cmd.ExtraFiles = []*os.File{writer}
	cmd.Stdout, cmd.Stderr = serviceLog{os.Stdout}, serviceLog{os.Stderr}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return errors.Join(err, writer.Close())
	}
	// The launcher holds its own copy of the write end. Ours has to close, or
	// the status reader would never see the end of the stream.
	if err := writer.Close(); err != nil {
		_ = cmd.Process.Kill() // the close failure is the error to report
		return errors.Join(err, cmd.Wait())
	}
	// On the way out the launcher has either exited already, when killing it
	// fails harmlessly, or it must not outlive the supervisor.
	defer func() { _ = cmd.Process.Kill() }()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	status := make(chan struct {
		process *os.Process
		err     error
	}, 1)
	go func() {
		var value struct {
			PID int `json:"child-pid"`
		}
		err := json.NewDecoder(io.LimitReader(reader, 4096)).Decode(&value)
		var process *os.Process
		if err == nil && value.PID > 0 {
			process, err = os.FindProcess(value.PID)
		} else if err == nil {
			err = errors.New("the Bubblewrap launcher did not report its server process")
		}
		status <- struct {
			process *os.Process
			err     error
		}{process, err}
		// Keep the status pipe open until exit so Bubblewrap can write its final
		// status without SIGPIPE. No application output travels through it.
		_, _ = io.Copy(io.Discard, reader) // drained, not read: errors mean nothing here
	}()
	var child *os.Process
	select {
	case value := <-status:
		if value.err != nil {
			return fmt.Errorf("starting the Bubblewrap sandbox: %w", value.err)
		}
		child = value.process
		// Release only frees the handle; the process itself is waited for
		// through the launcher.
		defer func() { _ = child.Release() }()
	case err := <-done:
		return fmt.Errorf("the Bubblewrap launcher exited before reporting its server: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return errors.New("the Bubblewrap launcher did not report its server within five seconds")
	}
	return waitForServer(ctx, child, done)
}

func waitForServer(ctx context.Context, child *os.Process, done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if err := child.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case err := <-done:
			return err
		case <-time.After(20 * time.Second):
			return fmt.Errorf("sandbox server did not stop within twenty seconds")
		}
	}
}
