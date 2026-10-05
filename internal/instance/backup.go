// SPDX-License-Identifier: AGPL-3.0-or-later
package instance

import (
	"context"
	"errors"

	"github.com/gofrs/flock"
)

type BackupLock struct {
	snapshot      *SnapshotLock
	compatibility *flock.Flock
}

// AcquireBackup holds the legacy server slot shared for the entire snapshot.
// Current servers also hold it shared and pin deletions with the object lock.
// Older servers hold it exclusively, so they can neither coexist with nor
// start halfway through a backup, even if a current server exits mid-copy.
func AcquireBackup(ctx context.Context, database string) (*BackupLock, error) {
	snapshot, err := AcquireSnapshot(ctx, database, true)
	if err != nil {
		return nil, err
	}
	compatibility := flock.New(snapshot.lifecycle.Path()+".server", flock.SetPermissions(0o600))
	ok, err := compatibility.TryRLock()
	if err != nil || !ok {
		return nil, errors.Join(errors.New("the running Imvault server does not support online snapshots; stop or upgrade it before backing up"), err, compatibility.Close(), snapshot.Close())
	}
	return &BackupLock{snapshot, compatibility}, nil
}

func (l *BackupLock) Close() error {
	return errors.Join(l.compatibility.Close(), l.snapshot.Close())
}
