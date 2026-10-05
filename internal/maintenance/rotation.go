// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"imvault/internal/closer"
	"imvault/internal/config"
	"imvault/internal/storage"
	"imvault/internal/store"
)

const scheduledTime = "20060102T150405.000000000Z"
const scheduledMarker = ".imvault-scheduled.json"

type retentionMarker struct {
	Version  int    `json:"version"`
	Instance string `json:"instance"`
}

// ScheduledBackup serializes backup and rotation in this destination. Only
// completed snapshots marked for this instance are eligible for retention.
// Manual backups are never marked. A failed backup never starts rotation.
func ScheduledBackup(ctx context.Context, cfg *config.Config, st *store.Store, objects storage.Backend, parent string, keep int, out io.Writer) (output string, err error) {
	if keep < 0 {
		return "", errors.New("backup retention must not be negative")
	}
	lock, err := lockBackupDestination(ctx, parent)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	instanceID, err := backupInstance(cfg.DBPath)
	if err != nil {
		return "", err
	}
	output = filepath.Join(parent, "imvault-"+time.Now().UTC().Format(scheduledTime))
	if err := Backup(ctx, cfg, st, objects, output, out); err != nil {
		return "", err
	}
	if err := markScheduledBackup(output, instanceID); err != nil {
		return output, err
	}
	if _, err := fmt.Fprintf(out, "Backup verified: %s\n", output); err != nil {
		return output, err
	}
	return output, pruneScheduledBackups(ctx, parent, filepath.Base(output), instanceID, keep, out)
}

func lockBackupDestination(ctx context.Context, parent string) (*flock.Flock, error) {
	parent = filepath.Clean(parent)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("backup destination must be a private directory (mode 0700), not a symlink")
	}
	lockPath := filepath.Join(parent, ".imvault-backup.lock")
	if info, err := os.Lstat(lockPath); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("backup lock must be a regular file, not a symlink")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	lock := flock.New(lockPath, flock.SetPermissions(0o600))
	ok, err := lock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil || !ok {
		return nil, errors.Join(errors.New("cannot lock backup destination"), err, ctx.Err(), lock.Close())
	}
	return lock, nil
}

func backupInstance(database string) (string, error) {
	path, err := filepath.Abs(database)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(path))), nil
}

func markScheduledBackup(output, instanceID string) (err error) {
	data, err := json.Marshal(retentionMarker{Version: 1, Instance: instanceID})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(output, scheduledMarker), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	dir, err := os.Open(output)
	if err != nil {
		return err
	}
	defer closer.Discard(dir)
	return dir.Sync()
}

func pruneScheduledBackups(ctx context.Context, parent, current, instanceID string, keep int, out io.Writer) error {
	if keep == 0 {
		return nil
	}
	names, err := scheduledSnapshots(parent, instanceID)
	if err != nil {
		return err
	}
	if !containsSnapshot(names, current) {
		return errors.New("new backup is not marked complete; refusing rotation")
	}
	older := make([]string, 0, len(names))
	for _, name := range names {
		if name != current {
			older = append(older, name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(older)))
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer closer.Discard(root)
	// Always retain the snapshot just made, even if the host clock moved back.
	for i := len(older) - 1; i >= keep-1; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := root.RemoveAll(older[i]); err != nil {
			return fmt.Errorf("rotate backup %s: %w", older[i], err)
		}
		if _, err := fmt.Fprintf(out, "Removed old scheduled backup: %s\n", older[i]); err != nil {
			return err
		}
	}
	dir, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer closer.Discard(dir)
	return dir.Sync()
}

func containsSnapshot(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

func scheduledSnapshots(parent, instanceID string) ([]string, error) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() || !scheduledName(entry.Name()) {
			continue
		}
		path := filepath.Join(parent, entry.Name())
		if !markedForInstance(path, instanceID) {
			continue
		}
		if _, err := readManifest(path); err == nil {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

func scheduledName(name string) bool {
	if !strings.HasPrefix(name, "imvault-") {
		return false
	}
	stamp := strings.TrimPrefix(name, "imvault-")
	parsed, err := time.Parse(scheduledTime, stamp)
	return err == nil && parsed.Format(scheduledTime) == stamp
}

func markedForInstance(path, instanceID string) bool {
	root, err := os.OpenRoot(path)
	if err != nil {
		return false
	}
	defer closer.Discard(root)
	f, err := root.Open(scheduledMarker)
	if err != nil {
		return false
	}
	defer closer.Discard(f)
	info, err := f.Stat()
	if err != nil || info.Size() > 1024 {
		return false
	}
	var marker retentionMarker
	decoder := json.NewDecoder(io.LimitReader(f, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return false
	}
	return decoder.Decode(new(any)) == io.EOF && marker.Version == 1 && marker.Instance == instanceID
}
