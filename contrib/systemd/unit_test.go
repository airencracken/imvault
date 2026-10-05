// SPDX-License-Identifier: AGPL-3.0-or-later

package systemd_test

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// unit is a parsed unit file: section, then key, then every value assigned.
type unit map[string]map[string][]string

func parseUnit(t *testing.T, path string) unit {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	parsed := unit{}
	section := ""
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		switch {
		case text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";"):
		case strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]"):
			section = strings.Trim(text, "[]")
			if parsed[section] == nil {
				parsed[section] = map[string][]string{}
			}
		default:
			key, value, ok := strings.Cut(text, "=")
			if !ok || section == "" {
				t.Fatalf("%s:%d: not an assignment in a section: %q", path, line, text)
			}
			parsed[section][key] = append(parsed[section][key], value)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return parsed
}

// The data directory holds credentials, the encryption key and private
// uploads, so both the directory and everything the server writes are the
// service account's alone.
func TestUnitKeepsTheDataDirectoryPrivate(t *testing.T) {
	service := parseUnit(t, "imvault.service")["Service"]
	for key, want := range map[string]string{
		"UMask":              "0077",
		"StateDirectory":     "imvault",
		"StateDirectoryMode": "0700",
		"User":               "imvault",
		"Group":              "imvault",
		"NoNewPrivileges":    "yes",
		"ProtectSystem":      "strict",
	} {
		// A later assignment would silently win, so each appears once.
		if got := service[key]; len(got) != 1 || got[0] != want {
			t.Errorf("%s = %q, want exactly %q", key, got, want)
		}
	}
	if got := service["Environment"]; !containsValue(got, "IMVAULT_DATA_DIR=/var/lib/imvault") {
		t.Errorf("the data directory is not the state directory: %q", got)
	}
}

func containsValue(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestBackupTimerUsesPrivateVerifiedSnapshotsWithoutStoppingServer(t *testing.T) {
	service := parseUnit(t, "imvault-backup.service")["Service"]
	for key, want := range map[string]string{"User": "imvault", "Group": "imvault", "UMask": "0077", "Type": "oneshot", "ExecStart": "/usr/local/bin/imvault backup --output-dir /var/backups/imvault --keep 7"} {
		if got := service[key]; len(got) != 1 || got[0] != want {
			t.Fatalf("backup %s=%v", key, got)
		}
	}
	if len(service["ExecStartPre"]) != 0 || len(service["ExecStopPost"]) != 0 {
		t.Fatal("backup must not stop or restart the server")
	}
	timer := parseUnit(t, "imvault-backup.timer")
	if len(timer["Timer"]["OnCalendar"]) != 1 || !containsValue(timer["Timer"]["Persistent"], "true") {
		t.Fatal("backup timer is not scheduled or persistent")
	}
}
