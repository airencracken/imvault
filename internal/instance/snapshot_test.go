// SPDX-License-Identifier: AGPL-3.0-or-later
package instance

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"imvault/internal/testutil"
)

func TestSnapshotPinsObjectsWithoutStoppingServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imvault.db")
	server, err := AcquireServer(path)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, server)
	pin, err := AcquireSnapshot(t.Context(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	normal, err := Acquire(path, false)
	if err != nil {
		t.Fatal("snapshot stopped ordinary database access", err)
	}
	testutil.Close(t, normal)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if deletion, err := AcquireSnapshot(ctx, path, false); err == nil {
		testutil.Close(t, deletion)
		t.Fatal("deletion bypassed snapshot")
	}
	if offline, err := Acquire(path, true); err == nil {
		testutil.Close(t, offline)
		t.Fatal("offline maintenance bypassed live snapshot")
	}
	testutil.Close(t, pin)
	deletion, err := AcquireSnapshot(t.Context(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, deletion)
}

func TestBackupRefusesOlderServerAndPinsOfflineServerSlot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imvault.db")
	lifecycle, err := Acquire(path, false)
	if err != nil {
		t.Fatal(err)
	}
	older := flock.New(lifecycle.Path()+".server", flock.SetPermissions(0o600))
	if ok, err := older.TryLock(); err != nil || !ok {
		t.Fatal(err)
	}
	if backup, err := AcquireBackup(t.Context(), path); err == nil {
		testutil.Close(t, backup)
		t.Fatal("older server admitted to live backup")
	}
	testutil.Close(t, older)
	testutil.Close(t, lifecycle)
	backup, err := AcquireBackup(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	older = flock.New(path+".maintenance.lock.server", flock.SetPermissions(0o600))
	if ok, err := older.TryLock(); err != nil || ok {
		t.Fatal("older server started during offline backup", err)
	}
	testutil.Close(t, older)
	testutil.Close(t, backup)
	server, err := AcquireServer(path)
	if err != nil {
		t.Fatal(err)
	}
	backup, err = AcquireBackup(t.Context(), path)
	if err != nil {
		t.Fatal("current server refused live backup", err)
	}
	testutil.Close(t, server)
	older = flock.New(path+".maintenance.lock.server", flock.SetPermissions(0o600))
	if ok, err := older.TryLock(); err != nil || ok {
		t.Fatal("older server started after current server exited during backup", err)
	}
	testutil.Close(t, older)
	testutil.Close(t, backup)
}
