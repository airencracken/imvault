// SPDX-License-Identifier: AGPL-3.0-or-later

package openrc_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDataDirectoryReachesDaemon(t *testing.T) {
	for _, override := range []string{"", "/srv/imvault data"} {
		t.Run(override, func(t *testing.T) {
			cmd := exec.Command("sh", "-c", `
if [ -n "$1" ]; then IMVAULT_DATA_DIR=$1; fi
. ./imvault || exit 1
checkpath() { return 0; }
start_pre || exit 1
sh -c 'printf "%s" "$IMVAULT_DATA_DIR"'
`, "test", override)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "RC_SVCNAME=imvault-audit-no-config"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("start_pre: %v: %s", err, out)
			}
			want := override
			if want == "" {
				want = "/var/lib/imvault"
			}
			if strings.TrimSpace(string(out)) != want {
				t.Fatalf("daemon data directory = %q, want %q", out, want)
			}
		})
	}
}

// A relative data directory is refused before checkpath can create and chown
// it relative to wherever the init system happens to be.
func TestRelativeDataDirectoryIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	cmd := exec.Command("sh", "-c", `
IMVAULT_DATA_DIR=relative/imvault
. ./imvault || exit 1
checkpath() { echo "checkpath $*"; return 0; }
eerror() { echo "error: $*"; }
start_pre
echo "status $?"
`)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "RC_SVCNAME=imvault-audit-no-config"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shell: %v: %s", err, out)
	}
	if strings.Contains(string(out), "checkpath") {
		t.Fatalf("checkpath ran for a relative path:\n%s", out)
	}
	if !strings.Contains(string(out), "status 1") || !strings.Contains(string(out), "absolute path") {
		t.Fatalf("a relative data directory was not refused:\n%s", out)
	}
}

// openrcShell sources the script with the given settings and runs body, with
// checkpath and eerror recorded rather than run.
func openrcShell(t *testing.T, env []string, body string) string {
	t.Helper()
	cmd := exec.Command("sh", "-c", `
. ./imvault || exit 1
checkpath() { echo "checkpath $*"; return 0; }
eerror() { echo "error: $*"; }
`+body)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "RC_SVCNAME=imvault-audit-no-config"}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shell: %v: %s", err, out)
	}
	return string(out)
}

// openrc-run hands umask to start-stop-daemon, and retry decides how long a
// stop waits before SIGKILL. The server drains requests for 15 seconds and
// the sandbox launcher waits up to 20 for it, so the TERM step must be longer
// than both.
func TestServiceVariablesKeepFilesPrivateAndAllowAGracefulStop(t *testing.T) {
	out := openrcShell(t, nil, `printf 'umask=%s\nretry=%s\n' "$umask" "$retry"`)
	if !strings.Contains(out, "umask=0077\n") {
		t.Errorf("umask is not 0077:\n%s", out)
	}
	if !strings.Contains(out, "retry=TERM/25/KILL/5\n") {
		t.Errorf("retry does not wait 25 seconds after TERM:\n%s", out)
	}
}

func TestDataDirectoryIsPrivate(t *testing.T) {
	out := openrcShell(t, nil, `start_pre || exit 1; echo "umask $(umask)"`)
	if !strings.Contains(out, "checkpath --directory --mode 0700 --owner imvault:imvault /var/lib/imvault\n") {
		t.Errorf("the data directory is not checked as 0700:\n%s", out)
	}
	if !strings.Contains(out, "umask 0077\n") {
		t.Errorf("start_pre does not apply the umask:\n%s", out)
	}
	if strings.Contains(out, "0750") || strings.Contains(out, "0755 /var/lib") {
		t.Errorf("an older, wider mode remains:\n%s", out)
	}
}

// Every path the script acts on must be absolute, and is checked before
// checkpath creates or chowns anything.
func TestRelativeServicePathsAreRefusedBeforeAnythingIsCreated(t *testing.T) {
	for _, name := range []string{"IMVAULT_DATA_DIR", "IMVAULT_BIN", "IMVAULT_LOG_FILE"} {
		for _, value := range []string{"relative/imvault", "./imvault", "../imvault", "~/imvault", " /leading-space", "-rf"} {
			t.Run(name+"="+value, func(t *testing.T) {
				out := openrcShell(t, []string{name + "=" + value}, `start_pre; echo "status $?"`)
				if strings.Contains(out, "checkpath") {
					t.Fatalf("checkpath ran for a relative path:\n%s", out)
				}
				if !strings.Contains(out, "status 1") || !strings.Contains(out, name+" must be an absolute path") {
					t.Fatalf("a relative %s was not refused:\n%s", name, out)
				}
			})
		}
	}
	// An explicitly empty setting is not absolute either.
	for _, name := range []string{"IMVAULT_DATA_DIR", "IMVAULT_BIN", "IMVAULT_LOG_FILE"} {
		out := openrcShell(t, nil, name+`=; start_pre; echo "status $?"`)
		if !strings.Contains(out, "status 1") || strings.Contains(out, "checkpath") {
			t.Errorf("an empty %s was not refused:\n%s", name, out)
		}
	}
	// Absolute paths, including ones with spaces, are accepted.
	out := openrcShell(t, []string{"IMVAULT_BIN=/opt/imvault bin/imvault", "IMVAULT_LOG_FILE=/var/log/imvault x.log"}, `start_pre; echo "status $?"`)
	if !strings.Contains(out, "status 0") {
		t.Fatalf("absolute paths were refused:\n%s", out)
	}
}

// A server that exits at once, such as one refusing a newer database, fails
// the start rather than being reported as started.
func TestStartWaitsToSeeTheServerStayUp(t *testing.T) {
	out := openrcShell(t, nil, `printf 'args=%s\n' "$start_stop_daemon_args"`)
	if !strings.Contains(out, "args=--wait 1000\n") {
		t.Errorf("start does not wait for the daemon:\n%s", out)
	}
}

// checkpath resets the mode of a directory it is given, so the existing log
// directory, /var/log by default, is left alone and only a missing one made.
func TestStartLeavesAnExistingLogDirectoryAlone(t *testing.T) {
	out := openrcShell(t, []string{"IMVAULT_LOG_FILE=/tmp/imvault.log"}, `start_pre || exit 1`)
	if strings.Contains(out, "checkpath --directory --mode 0755 /tmp\n") {
		t.Errorf("start_pre reset the mode of an existing log directory:\n%s", out)
	}
	missing := t.TempDir() + "/logs"
	out = openrcShell(t, []string{"IMVAULT_LOG_FILE=" + missing + "/imvault.log"}, `start_pre || exit 1`)
	if !strings.Contains(out, "checkpath --directory --mode 0755 "+missing+"\n") {
		t.Errorf("start_pre did not create a missing log directory:\n%s", out)
	}
}
