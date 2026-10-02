// SPDX-License-Identifier: AGPL-3.0-or-later

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dockerInstructions joins continued lines and drops comments, returning the
// instructions of the final (runtime) stage.
func dockerInstructions(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	var instructions []string
	current := ""
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || (trimmed == "" && current == "") {
			continue
		}
		if strings.HasSuffix(trimmed, "\\") {
			current += strings.TrimSuffix(trimmed, "\\") + " "
			continue
		}
		current += trimmed
		if strings.HasPrefix(strings.ToUpper(current), "FROM ") {
			instructions = nil
		}
		instructions = append(instructions, current)
		current = ""
	}
	return instructions
}

func healthcheck(t *testing.T) string {
	t.Helper()
	var found []string
	for _, instruction := range dockerInstructions(t) {
		if strings.HasPrefix(instruction, "HEALTHCHECK ") {
			found = append(found, instruction)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the runtime stage has %d HEALTHCHECK instructions, want 1", len(found))
	}
	return found[0]
}

// The image reports health from /healthz using the busybox wget it already
// has, rather than a package added for the purpose.
func TestDockerfileProbesHealthWithoutExtraPackages(t *testing.T) {
	check := healthcheck(t)
	for _, option := range []string{"--interval=", "--timeout=", "--start-period=", "--retries="} {
		if !strings.Contains(check, option) {
			t.Errorf("HEALTHCHECK does not set %s", option)
		}
	}
	if !strings.Contains(check, " CMD ") || !strings.Contains(check, "/healthz") || !strings.Contains(check, "wget ") {
		t.Errorf("HEALTHCHECK does not probe /healthz with wget: %s", check)
	}
	for _, instruction := range dockerInstructions(t) {
		if strings.HasPrefix(instruction, "RUN ") && regexp.MustCompile(`apk add[^&]*\b(curl|wget)\b`).MatchString(instruction) {
			t.Errorf("an HTTP client package is installed for the probe: %s", instruction)
		}
	}
}

// The probe follows IMVAULT_ADDR's port, never runs what the variable says,
// and passes a failure on.
func TestDockerHealthcheckCommand(t *testing.T) {
	check := healthcheck(t)
	command := check[strings.Index(check, " CMD ")+len(" CMD "):]
	for _, tc := range []struct {
		addr, wget string
		url        string
		status     int
	}{
		{":8080", "exit 0", "http://127.0.0.1:8080/healthz", 0},
		{"0.0.0.0:9090", "exit 0", "http://127.0.0.1:9090/healthz", 0},
		{"[::]:7000", "exit 0", "http://127.0.0.1:7000/healthz", 0},
		{"", "exit 0", "http://127.0.0.1:8080/healthz", 0},
		{":8080", "exit 1", "http://127.0.0.1:8080/healthz", 1},
		{":8080", "exit 7", "http://127.0.0.1:8080/healthz", 1},
		{"$(touch pwned):1", "exit 0", "http://127.0.0.1:1/healthz", 0},
		{"`touch pwned`;:2", "exit 0", "http://127.0.0.1:2/healthz", 0},
	} {
		dir := t.TempDir()
		log := filepath.Join(dir, "wget.log")
		stub := "#!/bin/sh\nfor arg; do last=$arg; done\nprintf '%s\\n' \"$last\" >> " + log + "\n" + tc.wget + "\n"
		if err := os.WriteFile(filepath.Join(dir, "wget"), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", "-c", command)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "IMVAULT_ADDR=" + tc.addr}
		err := cmd.Run()
		status := 0
		if exit, ok := err.(*exec.ExitError); ok {
			status = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		probed, _ := os.ReadFile(log)
		if status != tc.status || strings.TrimSpace(string(probed)) != tc.url {
			t.Errorf("IMVAULT_ADDR=%q: status %d, probed %q; want %d and %q", tc.addr, status, probed, tc.status, tc.url)
		}
		if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
			t.Errorf("IMVAULT_ADDR=%q ran a command", tc.addr)
		}
	}
}

// /data holds credentials and private uploads, so the image creates it for
// the service account alone, and runs as that account.
func TestDockerfileDataDirectoryIsPrivate(t *testing.T) {
	var run, user string
	for _, instruction := range dockerInstructions(t) {
		switch {
		case strings.HasPrefix(instruction, "RUN ") && strings.Contains(instruction, "/data"):
			run = instruction
		case strings.HasPrefix(instruction, "USER "):
			user = instruction
		}
	}
	for _, step := range []string{"chown -R imvault:imvault /data", "chmod 0700 /data"} {
		if !strings.Contains(run, step) {
			t.Errorf("the image does not %q: %s", step, run)
		}
	}
	if strings.Index(run, "chmod 0700 /data") < strings.Index(run, "adduser") {
		t.Error("/data is tightened before adduser creates it")
	}
	if user != "USER imvault" {
		t.Errorf("the image runs as %q", user)
	}
}
