// SPDX-License-Identifier: AGPL-3.0-or-later

package contrib

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/comfylib/proxyconfig"
)

// Imvault's own examples render for every server, with the domain, upstream
// and certificate paths replaced and the settings Imvault needs in the header.
func TestProxyConfigurations(t *testing.T) {
	for _, server := range []string{"caddy", "nginx", "apache"} {
		t.Run(server, func(t *testing.T) {
			config, err := proxyconfig.Render(ProxySpec, proxyconfig.Options{Server: server, Domain: "photos.example.net", Upstream: "[::1]:9100"})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"photos.example.net", "[::1]:9100", "IMVAULT_SECURE_COOKIES=true", "IMVAULT_BASE_URL=https://photos.example.net",
				"IMVAULT_TRUSTED_PROXIES=127.0.0.1/32,::1/128", "/etc/conf.d/imvault", "/etc/imvault/imvault.env"} {
				if !strings.Contains(config, want) {
					t.Errorf("missing %q in generated %s config", want, server)
				}
			}
			if strings.Contains(config, ProxySpec.ExampleDomain) || strings.Contains(config, ProxySpec.DefaultUpstream) {
				t.Fatal("generated config contains the original example's host or upstream")
			}
			if server != "caddy" && !strings.Contains(config, `"/etc/letsencrypt/live/photos.example.net/fullchain.pem"`) {
				t.Fatal("default certificate path does not follow the domain")
			}
		})
	}
}

// Each example must contain the placeholders Render replaces, or a generated
// configuration would quietly point at the example's host.
func TestExamplesCarryThePlaceholders(t *testing.T) {
	for _, path := range []string{"caddy/Caddyfile", "nginx/imvault.conf", "apache/imvault.conf"} {
		data, err := examples.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{ProxySpec.ExampleDomain, ProxySpec.DefaultUpstream} {
			if !bytes.Contains(data, []byte(want)) {
				t.Errorf("%s does not contain %q", path, want)
			}
		}
	}
}

// The command prints the configuration, and nothing for bad arguments.
func TestProxyConfigCLI(t *testing.T) {
	var output bytes.Buffer
	if err := proxyconfig.Run(ProxySpec, []string{"caddy", "--domain", "photos.example.net"}, &output); err != nil || !strings.Contains(output.String(), "reverse_proxy "+ProxySpec.DefaultUpstream) {
		t.Fatalf("render: %v, %s", err, &output)
	}
	for _, args := range [][]string{nil, {"unknown"}, {"caddy"}, {"nginx", "--domain", "bad\nserver{}"}} {
		output.Reset()
		if err := proxyconfig.Run(ProxySpec, args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("bad arguments emitted config: %q => %v, %s", args, err, &output)
		}
	}
}

func TestGeneratedCaddyConfigurationAdapts(t *testing.T) {
	binary := os.Getenv("CADDY_BINARY")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("caddy")
		if err != nil {
			t.Skip("install Caddy to validate its generated configuration")
		}
	}
	config, err := proxyconfig.Render(ProxySpec, proxyconfig.Options{Server: "caddy", Domain: "photos.example.net", Upstream: "[::1]:9100"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "adapt", "--adapter", "caddyfile", "--config", path).CombinedOutput(); err != nil {
		t.Fatalf("Caddy rejected generated configuration: %v\n%s", err, output)
	}
}
