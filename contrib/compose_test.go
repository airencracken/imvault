// SPDX-License-Identifier: AGPL-3.0-or-later

package contrib

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// In the compose stack Caddy runs in its own container, where 127.0.0.1 is
// Caddy itself. The Caddyfile it mounts must reach Imvault by service name on
// the port Imvault listens on, or the example proxies to nothing.
func TestComposeCaddyReachesTheImvaultService(t *testing.T) {
	compose := readContrib(t, "caddy/docker-compose.yml")
	mount := regexp.MustCompile(`(?m)^\s+- \./(\S+):/etc/caddy/Caddyfile:ro$`).FindStringSubmatch(compose)
	if mount == nil {
		t.Fatal("the caddy service does not mount a Caddyfile")
	}
	if mount[1] != "Caddyfile.compose" {
		t.Fatalf("the caddy service mounts %s, which is written for a host install", mount[1])
	}
	addr := regexp.MustCompile(`(?m)^\s+IMVAULT_ADDR: "(?:[^":]*):(\d+)"$`).FindStringSubmatch(compose)
	if addr == nil {
		t.Fatal("the imvault service sets no IMVAULT_ADDR port")
	}
	if !strings.Contains(compose, "\n  imvault:\n") {
		t.Fatal("the compose file has no imvault service")
	}

	caddyfile := readContrib(t, "caddy/Caddyfile.compose")
	upstreams := regexp.MustCompile(`(?m)^\s*reverse_proxy (\S+)`).FindAllStringSubmatch(caddyfile, -1)
	if len(upstreams) != 1 {
		t.Fatalf("Caddyfile.compose has %d reverse_proxy directives, want 1", len(upstreams))
	}
	if want := "imvault:" + addr[1]; upstreams[0][1] != want {
		t.Fatalf("Caddyfile.compose proxies to %s, want %s", upstreams[0][1], want)
	}

	// The host Caddyfile keeps its loopback upstream; only the copy differs.
	host := readContrib(t, "caddy/Caddyfile")
	if strings.ReplaceAll(stripComments(host), "127.0.0.1:8080", "imvault:8080") != stripComments(caddyfile) {
		t.Fatal("Caddyfile.compose differs from Caddyfile in more than its upstream")
	}

	binary, err := exec.LookPath("caddy")
	if err != nil {
		t.Skip("caddy is not installed")
	}
	if output, err := exec.Command(binary, "adapt", "--adapter", "caddyfile", "--config", filepath.Join("caddy", "Caddyfile.compose")).CombinedOutput(); err != nil {
		t.Fatalf("caddy rejects Caddyfile.compose: %v\n%s", err, output)
	}
}

func readContrib(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// stripComments drops comment lines and blank lines, so the comparison sees
// only directives.
func stripComments(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
