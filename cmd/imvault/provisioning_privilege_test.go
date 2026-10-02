package main

import (
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWithDataDirEnvironmentReplacesOnlyTheConfiguredKey(t *testing.T) {
	got := withDataDirEnvironment([]string{"PATH=/bin", "IMVAULT_DATA_DIR=/wrong", "HOME=/root"}, "IMVAULT_DATA_DIR", "/var/lib/imvault")
	want := []string{"PATH=/bin", "HOME=/root", "IMVAULT_DATA_DIR=/var/lib/imvault"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %#v, want %#v", got, want)
	}
}

func TestProvisioningDBPathForChildResolvesDefaultBeforeDroppingPrivileges(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "board-data")
	if got, want := provisioningDBPathForChild(dataDir, ""), filepath.Join(dataDir, "imvault.db"); got != want {
		t.Fatalf("default database path = %q, want %q", got, want)
	}
	if got := provisioningDBPathForChild(dataDir, "/srv/imvault/custom.db"); got != "/srv/imvault/custom.db" {
		t.Fatalf("configured database path = %q", got)
	}
}

func TestProvisioningHelpDoesNotReexecuteAsServiceUser(t *testing.T) {
	for _, args := range [][]string{{"create-admin", "--help"}, {"create-admin", "-h"}} {
		handled, status, err := reexecProvisioningAsService(args)
		if handled || status != 0 || err != nil {
			t.Fatalf("reexec for %v = handled %t status %d err %v", args, handled, status, err)
		}
	}
}

// asRoot pretends the process is root for the duration of a test.
func asRoot(t *testing.T) {
	t.Helper()
	previous := geteuid
	geteuid = func() int { return 0 }
	t.Cleanup(func() { geteuid = previous })
}

func TestCommandsThatWriteTheInstanceRunAsTheServiceUser(t *testing.T) {
	asRoot(t)
	for _, command := range []string{"create-admin", "backup", "migrate-storage", "rebuild-thumbnails", "refresh-metadata"} {
		if !shouldReexecProvisioning([]string{command}) {
			t.Errorf("%s would run as root", command)
		}
		if shouldReexecProvisioning([]string{command, "--help"}) {
			t.Errorf("%s --help would switch user", command)
		}
	}
	for _, command := range []string{"restore", "proxy-config", "help", "serve"} {
		if shouldReexecProvisioning([]string{command}) {
			t.Errorf("%s would switch to the service user", command)
		}
	}
}

func TestMaintenanceRefusesRootWithoutAServiceToBecome(t *testing.T) {
	asRoot(t)
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	for _, args := range [][]string{
		{"backup", "--output", filepath.Join(t.TempDir(), "out")},
		{"rebuild-thumbnails"},
		{"migrate-storage"},
		{"refresh-metadata"},
	} {
		err := runCommand(args, nil, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "must not run as root") {
			t.Errorf("%v as root: %v", args, err)
		}
	}
}
