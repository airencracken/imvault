// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The data and object directories are created for the service account alone,
// even without a restrictive umask, as in a container.
func TestEnsureDirsCreatesPrivateDirectories(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
	data := filepath.Join(t.TempDir(), "new", "data")
	cfg := &Config{DataDir: data, Storage: Storage{Driver: "disk"}}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Dir(data), data, filepath.Join(data, "objects")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s has mode %v", dir, info.Mode().Perm())
		}
	}
}
