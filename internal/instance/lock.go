// SPDX-License-Identifier: AGPL-3.0-or-later

// Package instance coordinates maintenance with the server and local commands.
package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

type ServerLock struct{ shared, process *flock.Flock }

func AcquireServer(database string) (*ServerLock, error) {
	shared, err := Acquire(database, false)
	if err != nil {
		return nil, err
	}
	process := flock.New(shared.Path()+".server", flock.SetPermissions(0o600))
	ok, err := process.TryLock()
	if err != nil || !ok {
		process.Close()
		shared.Close()
		return nil, fmt.Errorf("another imvault server is using this database")
	}
	return &ServerLock{shared, process}, nil
}

func (l *ServerLock) Close() error { return errors.Join(l.process.Close(), l.shared.Close()) }

// Acquire takes a shared lock for normal operation, or an exclusive lock for
// offline maintenance. Locks are released by the OS if a process exits.
func Acquire(database string, exclusive bool) (*flock.Flock, error) {
	abs, err := filepath.Abs(database)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	} else {
		dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
		if err != nil {
			return nil, err
		}
		abs = filepath.Join(dir, filepath.Base(abs))
	}
	lock := flock.New(abs+".maintenance.lock", flock.SetPermissions(0o600))
	var ok bool
	if exclusive {
		ok, err = lock.TryLock()
	} else {
		ok, err = lock.TryRLock()
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	if !ok {
		lock.Close()
		return nil, fmt.Errorf("instance is busy; stop imvault and other maintenance commands before retrying")
	}
	return lock, nil
}
