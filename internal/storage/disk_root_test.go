// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A symlink inside the object directory, however it got there, must not let a
// write, a read or a delete reach outside it.
func TestDiskOperationsStayInsideTheRoot(t *testing.T) {
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	disk, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(disk.Root(), "escape")); err != nil {
		t.Fatal(err)
	}

	if _, err := disk.Save(t.Context(), "escape/planted", strings.NewReader("x")); err == nil {
		t.Error("a write followed a symlink out of the root")
	}
	if _, err := os.Stat(filepath.Join(outside, "planted")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a file was created outside the root")
	}
	if err := disk.Delete(t.Context(), "escape/victim"); err == nil {
		t.Error("a delete followed a symlink out of the root")
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep me" {
		t.Fatalf("the file outside the root was touched: %q %v", data, err)
	}
	if _, err := disk.Open(t.Context(), "escape/victim"); err == nil {
		t.Error("a read followed a symlink out of the root")
	}
}

func TestDiskSaveLeavesNoScratchFiles(t *testing.T) {
	disk, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disk.Save(t.Context(), "a/b/object", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatal(err)
	}
	if _, err := disk.Save(t.Context(), "a/b/failed", failingReader{}); err == nil {
		t.Fatal("a failed source was reported as saved")
	}
	entries, err := os.ReadDir(filepath.Join(disk.Root(), "a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "object" {
		t.Fatalf("unexpected files after a failed save: %v", entries)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("source broke") }

// Object directories are private to the service account, whatever the umask.
func TestDiskCreatesPrivateDirectories(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
	root := filepath.Join(t.TempDir(), "objects")
	disk, err := NewDisk(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disk.Save(t.Context(), "orig/ab/cd.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, filepath.Join(root, "orig"), filepath.Join(root, "orig", "ab")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s has mode %v", dir, info.Mode().Perm())
		}
	}
}
