// SPDX-License-Identifier: AGPL-3.0-or-later
package instance

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/flock"
)

// SnapshotLock pins stored objects while a database snapshot is copied. The
// lifecycle lock excludes offline maintenance; the object lock excludes only
// deletions, so a live server can keep accepting uploads and serving photos.
// Every process must use these locks; older binaries must be stopped first.
type SnapshotLock struct{ lifecycle, objects *flock.Flock }

func AcquireSnapshot(ctx context.Context, database string, exclusive bool) (*SnapshotLock, error) {
	lifecycle, err := Acquire(database, false)
	if err != nil {
		return nil, err
	}
	objects := flock.New(lifecycle.Path()+".objects", flock.SetPermissions(0o600))
	var ok bool
	if exclusive {
		ok, err = objects.TryLockContext(ctx, 100*time.Millisecond)
	} else {
		ok, err = objects.TryRLockContext(ctx, 100*time.Millisecond)
	}
	if err != nil || !ok {
		return nil, errors.Join(err, ctx.Err(), objects.Close(), lifecycle.Close())
	}
	return &SnapshotLock{lifecycle, objects}, nil
}
func (l *SnapshotLock) Close() error { return errors.Join(l.objects.Close(), l.lifecycle.Close()) }
