// SPDX-License-Identifier: AGPL-3.0-or-later

// Package storage abstracts where uploaded objects live.
package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when an object key does not exist.
var ErrNotFound = errors.New("storage: object not found")

// Backend is the minimal object store the application needs. The local disk
// implementation below is the only one today; an S3 backend can slot in here
// without touching handlers.
type Backend interface {
	// Save writes the object, replacing any existing data, and reports how
	// many bytes were written.
	Save(key string, r io.Reader) (int64, error)
	// Open returns a readable, seekable handle for the object.
	Open(key string) (io.ReadSeekCloser, error)
	// Delete removes the object. Missing objects are not an error.
	Delete(key string) error
	// LocalPath returns a filesystem path if the backend is disk-backed.
	LocalPath(key string) (string, bool)
}

// Disk stores objects as files beneath a root directory.
type Disk struct {
	root string
}

// NewDisk creates a disk backend rooted at root.
func NewDisk(root string) (*Disk, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create root: %w", err)
	}
	return &Disk{root: abs}, nil
}

// Root returns the absolute root directory.
func (d *Disk) Root() string { return d.root }

// Save streams r into the object at key.
func (d *Disk) Save(key string, r io.Reader) (int64, error) {
	full, err := d.resolve(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return 0, fmt.Errorf("storage: create dir for %s: %w", key, err)
	}

	// Write to a sibling temp file first so a failed upload never leaves a
	// truncated object behind under the real key.
	tmp, err := os.CreateTemp(filepath.Dir(full), ".upload-*")
	if err != nil {
		return 0, fmt.Errorf("storage: temp file for %s: %w", key, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmp != nil {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	n, err := io.Copy(tmp, r)
	if err != nil {
		return 0, fmt.Errorf("storage: write %s: %w", key, err)
	}
	if err := tmp.Sync(); err != nil {
		return 0, fmt.Errorf("storage: sync %s: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("storage: close %s: %w", key, err)
	}
	tmp = nil

	if err := os.Rename(tmpName, full); err != nil {
		os.Remove(tmpName)
		return 0, fmt.Errorf("storage: commit %s: %w", key, err)
	}
	return n, nil
}

// Open returns a handle to the object at key.
func (d *Disk) Open(key string) (io.ReadSeekCloser, error) {
	full, err := d.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: open %s: %w", key, err)
	}
	return f, nil
}

// Delete removes the object at key, ignoring missing files.
func (d *Disk) Delete(key string) error {
	full, err := d.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage: delete %s: %w", key, err)
	}
	return nil
}

// LocalPath reports the filesystem path for key.
func (d *Disk) LocalPath(key string) (string, bool) {
	full, err := d.resolve(key)
	if err != nil {
		return "", false
	}
	return full, true
}

// resolve maps an object key to an absolute path, rejecting anything that
// tries to escape the root.
func (d *Disk) resolve(key string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(key, "\\", "/"))
	if clean == "/" {
		return "", fmt.Errorf("storage: empty object key")
	}
	full := filepath.Join(d.root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))

	// Defence in depth: the cleaned path must still sit under root.
	if full != d.root && !strings.HasPrefix(full, d.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("storage: key %q escapes root", key)
	}
	return full, nil
}
