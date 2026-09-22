// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"path/filepath"
	"testing"
)

func TestMaintenanceExcludesServerAndOtherCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imvault.db")
	server, err := Acquire(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	admin, err := Acquire(path, false)
	if err != nil {
		t.Fatal("ordinary local commands should coexist", err)
	}
	admin.Close()
	if lock, err := Acquire(path, true); err == nil {
		lock.Close()
		t.Fatal("maintenance ran beside the server")
	}
	server.Close()
	maintenance, err := Acquire(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Close()
	if lock, err := Acquire(path, false); err == nil {
		lock.Close()
		t.Fatal("server started during maintenance")
	}
	if lock, err := Acquire(path, true); err == nil {
		lock.Close()
		t.Fatal("two maintenance commands ran together")
	}
}

func TestOnlyOneServerMayUseADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imvault.db")
	first, err := AcquireServer(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := AcquireServer(path); err == nil {
		second.Close()
		t.Fatal("second server was allowed")
	}
	admin, err := Acquire(path, false)
	if err != nil {
		t.Fatal("server prevented ordinary local command", err)
	}
	admin.Close()
}
