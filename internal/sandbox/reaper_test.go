// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// waitForZombie polls until pid has exited and is waiting to be collected.
func waitForZombie(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if zombieChildOf(pid, os.Getpid()) {
			return
		}
	}
	t.Fatalf("process %d never became a zombie", pid)
}

func TestReaperCollectsUntrackedZombies(t *testing.T) {
	orphan := exec.Command("true")
	if err := orphan.Start(); err != nil {
		t.Fatal(err)
	}
	waitForZombie(t, orphan.Process.Pid)
	if n := ReapOrphans(); n < 1 {
		t.Fatalf("reaped %d processes, want the zombie", n)
	}
	if zombieChildOf(orphan.Process.Pid, os.Getpid()) {
		t.Fatal("the zombie is still there")
	}
}

func TestReaperLeavesTrackedChildrenForTheirOwner(t *testing.T) {
	child := exec.Command("true")
	owned.Lock()
	if err := child.Start(); err != nil {
		owned.Unlock()
		t.Fatal(err)
	}
	owned.pids[child.Process.Pid] = struct{}{}
	owned.Unlock()
	t.Cleanup(func() {
		owned.Lock()
		delete(owned.pids, child.Process.Pid)
		owned.Unlock()
	})

	waitForZombie(t, child.Process.Pid)
	ReapOrphans()
	if !zombieChildOf(child.Process.Pid, os.Getpid()) {
		t.Fatal("the reaper took a child that os/exec still has to wait for")
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("the owner could not wait for its child: %v", err)
	}
}

func TestRunChildSurvivesAConcurrentReaper(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				ReapOrphans()
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for i := 0; i < 50; i++ {
		if err := RunChild(exec.Command("true")); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		var exit *exec.ExitError
		if err := RunChild(exec.Command("false")); !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatalf("run %d lost its exit status: %v", i, err)
		}
	}
}

func TestZombieDetectionReadsTheRightFields(t *testing.T) {
	if zombieChildOf(os.Getpid(), os.Getppid()) {
		t.Fatal("a running process was read as a zombie")
	}
	if zombieChildOf(-1, 1) || zombieChildOf(1<<30, 1) {
		t.Fatal("a missing process was read as a zombie")
	}
}

// TestRealOrphanedMediaJobsAreReaped reproduces the leak end to end: this test
// binary runs as PID 1 inside Bubblewrap, starts nested media-style sandboxes,
// and kills their launchers the way a timeout does.
func TestRealOrphanedMediaJobsAreReaped(t *testing.T) {
	if os.Getenv("IMVAULT_REAPER_CHILD") == "1" {
		orphanAndReap(t)
		return
	}
	if os.Getenv("COMFYWARE_SANDBOX_TEST") != "1" {
		t.Skip("set COMFYWARE_SANDBOX_TEST=1 to require real Linux namespace tests")
	}
	bwrap, err := Binary("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bwrap, "--as-pid-1", "--die-with-parent", "--unshare-user", "--unshare-pid",
		"--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev",
		"--", self, "-test.run=^TestRealOrphanedMediaJobsAreReaped$", "-test.v")
	cmd.Env = append(os.Environ(), "IMVAULT_REAPER_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "zombies after reaping: 0") {
		t.Fatalf("orphaned jobs were not reaped (%v):\n%s", err, out)
	}
}

func orphanAndReap(t *testing.T) {
	if os.Getpid() != 1 {
		t.Fatalf("the helper is PID %d, not 1", os.Getpid())
	}
	bwrap, err := Binary("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	args, err := Base(false)
	if err != nil {
		t.Fatal(err)
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		job := exec.Command(bwrap, append(append([]string{}, args...), "--", sleep, "30")...)
		job.Env = RuntimeEnv()
		if err := job.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		if err := job.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := job.Wait(); err == nil {
			t.Fatal("the killed launcher reported success")
		}
	}
	time.Sleep(300 * time.Millisecond)
	if before := countZombies(t); before == 0 {
		t.Fatal("no orphan was left behind, so this proves nothing")
	}
	ReapOrphans()
	t.Logf("zombies after reaping: %d", countZombies(t))
}

func countZombies(t *testing.T) int {
	t.Helper()
	entries, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		pid, err := strconv.Atoi(filepath.Base(entry))
		if err == nil && zombieChildOf(pid, os.Getpid()) {
			n++
		}
	}
	return n
}
