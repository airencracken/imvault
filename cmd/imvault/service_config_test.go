package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProvisioningDataDir(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", "")
	t.Run("portable default", func(t *testing.T) {
		got, err := resolveProvisioningDataDir(provisioningConfigPaths{serviceDefault: "/var/lib/imvault"})
		if err != nil || got != "./data" {
			t.Fatalf("resolve = %q, %v; want ./data", got, err)
		}
	})
	t.Run("OpenRC conf.d literal", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "service data")
		config := filepath.Join(t.TempDir(), "imvault.confd")
		if err := os.WriteFile(config, []byte("IMVAULT_DATA_DIR=\""+want+"\" # comment\n"), 0600); err != nil {
			t.Fatal(err)
		}
		paths := provisioningConfigPaths{openRCConfig: config, openRCInstalled: true, openRCActive: true, serviceDefault: "/var/lib/imvault"}
		got, err := resolveProvisioningDataDir(paths)
		if err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("systemd environment file overrides unit", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "systemd data")
		envFile := filepath.Join(t.TempDir(), "imvault.env")
		if err := os.WriteFile(envFile, []byte("IMVAULT_DATA_DIR='"+want+"'\n"), 0600); err != nil {
			t.Fatal(err)
		}
		unit := filepath.Join(t.TempDir(), "imvault.service")
		contents := "[Service]\nEnvironment=IMVAULT_DATA_DIR=/var/lib/imvault\nEnvironmentFile=-" + envFile + "\n"
		if err := os.WriteFile(unit, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		paths := provisioningConfigPaths{systemdUnit: unit, systemdActive: true, serviceDefault: "/var/lib/imvault"}
		got, err := resolveProvisioningDataDir(paths)
		if err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("explicit environment wins", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "explicit")
		t.Setenv("IMVAULT_DATA_DIR", want)
		got, err := resolveProvisioningDataDir(provisioningConfigPaths{serviceDefault: "/var/lib/imvault"})
		if err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
}

func TestResolveProvisioningDataDirRefusesAmbiguousOrDynamicConfig(t *testing.T) {
	t.Setenv("IMVAULT_DATA_DIR", "")
	openRC := filepath.Join(t.TempDir(), "openrc.conf")
	unit := filepath.Join(t.TempDir(), "imvault.service")
	if err := os.WriteFile(openRC, []byte("IMVAULT_DATA_DIR=/var/lib/openrc\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("[Service]\nEnvironment=IMVAULT_DATA_DIR=/var/lib/systemd\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := provisioningConfigPaths{openRCConfig: openRC, openRCInstalled: true, systemdUnit: unit, serviceDefault: "/var/lib/imvault"}
	if _, err := resolveProvisioningDataDir(paths); err == nil || !strings.Contains(err.Error(), "different Imvault data directories") {
		t.Fatalf("ambiguous config error = %v", err)
	}
	if err := os.WriteFile(openRC, []byte("IMVAULT_DATA_DIR=\"${ROOT}/data\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths.systemdUnit = ""
	if _, err := resolveProvisioningDataDir(paths); err == nil || !strings.Contains(err.Error(), "shell expression") {
		t.Fatalf("dynamic config error = %v", err)
	}
}

func TestResolveProvisioningDBPath(t *testing.T) {
	t.Setenv("IMVAULT_DB", "")
	openRC := filepath.Join(t.TempDir(), "imvault.confd")
	if err := os.WriteFile(openRC, []byte("IMVAULT_DB=/var/lib/imvault/custom.db\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := provisioningConfigPaths{openRCConfig: openRC, openRCInstalled: true, openRCActive: true}
	got, found, err := resolveProvisioningDBPath(paths)
	if err != nil || !found || got != "/var/lib/imvault/custom.db" {
		t.Fatalf("OpenRC database path = %q found=%t err=%v", got, found, err)
	}

	unit := filepath.Join(t.TempDir(), "imvault.service")
	envFile := filepath.Join(t.TempDir(), "imvault.env")
	if err := os.WriteFile(envFile, []byte("IMVAULT_DB=/srv/imvault/accounts.db\n"), 0600); err != nil {
		t.Fatal(err)
	}
	contents := "[Service]\nEnvironmentFile=-" + envFile + "\n"
	if err := os.WriteFile(unit, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	paths = provisioningConfigPaths{systemdUnit: unit, systemdActive: true}
	got, found, err = resolveProvisioningDBPath(paths)
	if err != nil || !found || got != "/srv/imvault/accounts.db" {
		t.Fatalf("systemd database path = %q found=%t err=%v", got, found, err)
	}

	t.Setenv("IMVAULT_DB", "/tmp/explicit.db")
	got, found, err = resolveProvisioningDBPath(paths)
	if err != nil || !found || got != "/tmp/explicit.db" {
		t.Fatalf("explicit database path = %q found=%t err=%v", got, found, err)
	}
}

func TestResolveProvisioningServiceAccount(t *testing.T) {
	openRC := filepath.Join(t.TempDir(), "imvault.confd")
	if err := os.WriteFile(openRC, []byte("IMVAULT_USER=board-user\nIMVAULT_GROUP=board-group\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := provisioningConfigPaths{openRCConfig: openRC, openRCInstalled: true, openRCActive: true}
	user, group, managed, err := resolveProvisioningServiceAccount(paths)
	if err != nil || !managed || user != "board-user" || group != "board-group" {
		t.Fatalf("service account = %q:%q managed=%t err=%v", user, group, managed, err)
	}

	paths = provisioningConfigPaths{openRCInstalled: true}
	user, group, managed, err = resolveProvisioningServiceAccount(paths)
	if err != nil || !managed || user != "imvault" || group != "imvault" {
		t.Fatalf("default service account = %q:%q managed=%t err=%v", user, group, managed, err)
	}

	user, group, managed, err = resolveProvisioningServiceAccount(provisioningConfigPaths{})
	if err != nil || managed || user != "" || group != "" {
		t.Fatalf("portable install identity = %q:%q managed=%t err=%v", user, group, managed, err)
	}
}
