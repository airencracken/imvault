// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A server confined by Run is PID 1 in its namespace, so every process that
// loses its parent inside the namespace becomes its child. The usual one is a
// media job's Bubblewrap init, orphaned when a timeout kills the launcher above
// it. Nothing else will ever wait for it, so without a reaper each timeout
// leaves a zombie until the server restarts.
//
// The reaper must not take a child that os/exec is about to wait for, or that
// Wait would fail. Every process the server starts goes through RunChild, which
// records the PID before the child can exit; the reaper only collects zombies
// that are not recorded.
var owned = struct {
	sync.Mutex
	pids map[int]struct{}
}{pids: map[int]struct{}{}}

// RunChild runs cmd as Run would, keeping its PID out of the reaper's reach
// until os/exec has waited for it.
func RunChild(cmd *exec.Cmd) error {
	owned.Lock()
	err := cmd.Start()
	if err == nil {
		owned.pids[cmd.Process.Pid] = struct{}{}
	}
	owned.Unlock()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	owned.Lock()
	delete(owned.pids, cmd.Process.Pid)
	owned.Unlock()
	return err
}

// StartReaper collects orphaned children until ctx ends. It does nothing
// unless this process is PID 1, which is the only case where orphans arrive.
func StartReaper(ctx context.Context) {
	if os.Getpid() != 1 {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGCHLD)
	go func() {
		defer signal.Stop(signals)
		// SIGCHLD is coalesced, so a periodic pass catches anything a
		// merged signal hid.
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-signals:
			case <-ticker.C:
			}
			ReapOrphans()
		}
	}()
}

// ReapOrphans waits for every zombie child of this process that RunChild did
// not start, and reports how many it collected.
func ReapOrphans() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	self := os.Getpid()
	reaped := 0
	owned.Lock()
	defer owned.Unlock()
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if _, mine := owned.pids[pid]; mine || !zombieChildOf(pid, self) {
			continue
		}
		var status syscall.WaitStatus
		if got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); err == nil && got == pid {
			reaped++
		}
	}
	return reaped
}

// zombieChildOf reads /proc/<pid>/stat for the state and parent fields. The
// command name may contain spaces and parentheses, so the fields are counted
// from the last closing parenthesis.
func zombieChildOf(pid, parent int) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	stat := string(raw)
	end := strings.LastIndexByte(stat, ')')
	if end < 0 || end+2 > len(stat) {
		return false
	}
	fields := strings.Fields(stat[end+2:])
	return len(fields) >= 2 && fields[0] == "Z" && fields[1] == strconv.Itoa(parent)
}
