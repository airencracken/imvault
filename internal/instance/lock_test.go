// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"path/filepath"
	"testing"

	"imvault/internal/testutil"
)

func TestMaintenanceExcludesServerAndOtherCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imvault.db")
	server, err := Acquire(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, server)
	admin, err := Acquire(path, false)
	if err != nil {
		t.Fatal("ordinary local commands should coexist", err)
	}
	testutil.Close(t, admin)
	if lock, err := Acquire(path, true); err == nil {
		testutil.Close(t, lock)
		t.Fatal("maintenance ran beside the server")
	}
	testutil.Close(t, server)
	maintenance, err := Acquire(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, maintenance)
	if lock, err := Acquire(path, false); err == nil {
		testutil.Close(t, lock)
		t.Fatal("server started during maintenance")
	}
	if lock, err := Acquire(path, true); err == nil {
		testutil.Close(t, lock)
		t.Fatal("two maintenance commands ran together")
	}
}

func TestOnlyOneServerMayUseADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imvault.db")
	first, err := AcquireServer(path)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, first)
	if second, err := AcquireServer(path); err == nil {
		testutil.Close(t, second)
		t.Fatal("second server was allowed")
	}
	admin, err := Acquire(path, false)
	if err != nil {
		t.Fatal("server prevented ordinary local command", err)
	}
	testutil.Close(t, admin)
}
