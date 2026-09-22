// SPDX-License-Identifier: AGPL-3.0-or-later

package logrotate_test

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type rotationFixture struct {
	tool, config, state, log string
}

func newRotationFixture(t *testing.T) rotationFixture {
	t.Helper()
	tool, err := exec.LookPath("logrotate")
	if err != nil {
		t.Skip("install logrotate to run rotation integration tests")
	}
	rule, err := os.ReadFile("imvault")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "logs with spaces")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	f := rotationFixture{tool, filepath.Join(dir, "rule"), filepath.Join(dir, "state"), filepath.Join(dir, "imvault.log")}
	// A host's dateext default must not prevent several size-based rotations
	// on the same day. Only replace the log path in the shipped rule.
	config := "dateext\n" + strings.ReplaceAll(string(rule), "/var/log/imvault.log", f.log)
	if err := os.WriteFile(f.config, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f rotationFixture) run(t *testing.T, args ...string) {
	t.Helper()
	args = append(args, "--state", f.state, f.config)
	if out, err := exec.Command(f.tool, args...).CombinedOutput(); err != nil {
		t.Fatalf("logrotate: %v: %s", err, out)
	}
}

func TestRotationPreservesOpenWriterAndBoundsArchives(t *testing.T) {
	f := newRotationFixture(t)
	f.run(t, "--force") // a missing log is harmless
	writer, err := os.OpenFile(f.log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	before, err := writer.Stat()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 15; i++ {
		if _, err := fmt.Fprintf(writer, "record %d\n", i); err != nil {
			t.Fatal(err)
		}
		f.run(t, "--force")
		current, err := os.Stat(f.log)
		if err != nil || !os.SameFile(before, current) || current.Size() != 0 || current.Mode().Perm() != before.Mode().Perm() {
			t.Fatal("rotation replaced the open file or changed its permissions")
		}
		archive, err := os.ReadFile(f.log + ".1")
		if err != nil || string(archive) != fmt.Sprintf("record %d\n", i) {
			t.Fatalf("open writer did not survive rotation %d: %q (%v)", i, archive, err)
		}
	}
	f.run(t, "--force") // empty logs must not consume another archive slot
	archives, err := filepath.Glob(f.log + ".*")
	if err != nil || len(archives) != 14 {
		t.Fatalf("archives = %v (%v), want 14", archives, err)
	}
	oldest, err := os.Open(f.log + ".14.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer oldest.Close()
	compressed, err := gzip.NewReader(oldest)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	data, err := io.ReadAll(compressed)
	if err != nil || string(data) != "record 2\n" {
		t.Fatalf("oldest retained archive = %q (%v)", data, err)
	}
}

func TestRotationRunsDailyOrAboveTenMiB(t *testing.T) {
	f := newRotationFixture(t)
	if err := os.WriteFile(f.log, []byte("first\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	if _, err := os.Stat(f.log + ".1"); !os.IsNotExist(err) {
		t.Fatal("small new log rotated immediately")
	}
	if err := os.Truncate(f.log, (10<<20)+1); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	archive, err := os.Stat(f.log + ".1")
	if err != nil || archive.Size() != (10<<20)+1 {
		t.Fatal("size threshold did not rotate before the daily interval")
	}
	if err := os.WriteFile(f.log, []byte("daily\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	state := fmt.Sprintf("logrotate state -- version 2\n%q %s\n", f.log, time.Now().Add(-48*time.Hour).Format("2006-1-2-15:4:5"))
	if err := os.WriteFile(f.state, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	f.run(t, "--debug")
	data, err := os.ReadFile(f.log)
	if err != nil || string(data) != "daily\n" {
		t.Fatal("debug validation changed the log")
	}
	f.run(t)
	data, err = os.ReadFile(f.log + ".1")
	if err != nil || string(data) != "daily\n" {
		t.Fatal("daily interval did not rotate a small log")
	}
}
