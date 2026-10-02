// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/airencracken/comfylib/svcconfig"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestServicePathsDescribeImvault(t *testing.T) {
	paths := servicePaths()
	if paths.Name != "Imvault" || paths.Prefix != "IMVAULT_" || paths.DefaultDataDir != "/var/lib/imvault" {
		t.Fatalf("servicePaths = %+v", paths)
	}
	if paths.OpenRCConfig != "/etc/conf.d/imvault" {
		t.Fatalf("OpenRC configuration at %q", paths.OpenRCConfig)
	}
}

// Exactly the commands that write the live instance switch to the service
// account. restore writes a new directory and must stay with the operator.
func TestCommandsThatWriteTheInstanceRunAsTheServiceUser(t *testing.T) {
	want := []string{"backup", "create-admin", "migrate-storage", "rebuild-thumbnails", "refresh-metadata"}
	if got := slices.Sorted(maps.Keys(serviceUserCommands)); !slices.Equal(got, want) {
		t.Fatalf("service-user commands = %v, want %v", got, want)
	}
	for _, command := range want {
		if !serviceUserCommands[command] {
			t.Errorf("%s is listed but not enabled", command)
		}
	}
}

// The database a re-run command opens is resolved before privileges drop,
// because the service account usually cannot read the configuration.
func TestChildDatabaseIsResolvedBeforeDroppingPrivileges(t *testing.T) {
	t.Setenv("IMVAULT_DB", "")
	dataDir := filepath.Join(t.TempDir(), "board-data")

	got, err := childDBPath(imvaultPaths(svcconfig.Paths{}), dataDir)
	if want := filepath.Join(dataDir, "imvault.db"); err != nil || got != want {
		t.Fatalf("default database = %q, %v; want %q", got, err, want)
	}

	openRC := filepath.Join(t.TempDir(), "imvault.confd")
	writeFile(t, openRC, "IMVAULT_DB=/var/lib/imvault/custom.db\n")
	got, err = childDBPath(imvaultPaths(svcconfig.Paths{OpenRCConfig: openRC, OpenRCInstalled: true, OpenRCActive: true}), dataDir)
	if err != nil || got != "/var/lib/imvault/custom.db" {
		t.Fatalf("OpenRC database = %q, %v", got, err)
	}

	t.Setenv("IMVAULT_DB", "/srv/explicit.db")
	got, err = childDBPath(imvaultPaths(svcconfig.Paths{OpenRCConfig: openRC, OpenRCInstalled: true, OpenRCActive: true}), dataDir)
	if err != nil || got != "/srv/explicit.db" {
		t.Fatalf("explicit database = %q, %v", got, err)
	}
}

// systemd takes EnvironmentFile= as one literal path. Imvault used to split it
// into words, so a path with a space was misread.
func TestASystemdEnvironmentFileMayContainSpaces(t *testing.T) {
	t.Setenv("IMVAULT_DB", "")
	envFile := filepath.Join(t.TempDir(), "service settings", "imvault.env")
	writeFile(t, envFile, "IMVAULT_DB=/srv/imvault/accounts.db\n")
	unit := filepath.Join(t.TempDir(), "imvault.service")
	writeFile(t, unit, "[Service]\nEnvironmentFile=-"+envFile+"\n")
	got, err := childDBPath(imvaultPaths(svcconfig.Paths{SystemdUnit: unit, SystemdActive: true}), "/var/lib/imvault")
	if err != nil || got != "/srv/imvault/accounts.db" {
		t.Fatalf("database from %q = %q, %v", envFile, got, err)
	}
}

func TestServiceConfigurationThatNeedsEvaluationIsRefused(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", "")
	t.Setenv("IMVAULT_DB", "")
	for name, contents := range map[string]string{
		"shell expansion":   "IMVAULT_DATA_DIR=\"${ROOT}/data\"\n",
		"command":           "IMVAULT_DATA_DIR=$(cat /etc/where)\n",
		"relative":          "IMVAULT_DATA_DIR=data\n",
		"unterminated":      "IMVAULT_DATA_DIR=\"/var/lib/imvault\n",
		"database variable": "IMVAULT_DB=\"$HOME/imvault.db\"\n",
	} {
		openRC := filepath.Join(t.TempDir(), "imvault.confd")
		writeFile(t, openRC, contents)
		paths := imvaultPaths(svcconfig.Paths{OpenRCConfig: openRC, OpenRCInstalled: true, OpenRCActive: true})
		_, dataErr := paths.DataDir("IMVAULT_DATA_DIR")
		_, dbErr := childDBPath(paths, "/var/lib/imvault")
		if dataErr == nil && dbErr == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestMaintenanceRefusesRootWithoutAServiceToBecome(t *testing.T) {
	previous := geteuid
	geteuid = func() int { return 0 }
	t.Cleanup(func() { geteuid = previous })
	t.Setenv("IMVAULT_DATA_DIR", t.TempDir())
	for _, args := range [][]string{
		{"backup", "--output", filepath.Join(t.TempDir(), "out")},
		{"rebuild-thumbnails"},
		{"migrate-storage"},
		{"refresh-metadata"},
	} {
		err := runCommand(args, nil, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "must not run as root") || !strings.Contains(err.Error(), "sudo -u imvault env IMVAULT_DATA_DIR=/var/lib/imvault imvault "+args[0]) {
			t.Errorf("%v as root: %v", args, err)
		}
	}
	geteuid = func() int { return 1000 }
	if err := refuseRootMaintenance("backup"); err != nil {
		t.Fatalf("an unprivileged user was refused: %v", err)
	}
}

// A systemd specifier would need systemd to expand it, so it is refused with a
// hint rather than read as a literal path.
func TestASystemdSpecifierInEnvironmentFileIsRefused(t *testing.T) {
	t.Setenv("IMVAULT_DB", "")
	unit := filepath.Join(t.TempDir(), "imvault.service")
	writeFile(t, unit, "[Service]\nEnvironmentFile=-/etc/%N/imvault.env\n")
	_, err := childDBPath(imvaultPaths(svcconfig.Paths{SystemdUnit: unit, SystemdActive: true}), "/var/lib/imvault")
	if err == nil || !strings.Contains(err.Error(), "IMVAULT_DB") {
		t.Fatalf("a specifier was accepted or the hint is missing: %v", err)
	}
}
