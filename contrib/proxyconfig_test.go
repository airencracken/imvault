package proxyconfig

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProxyConfigurations(t *testing.T) {
	for _, server := range []string{"caddy", "nginx", "apache"} {
		t.Run(server, func(t *testing.T) {
			config, err := Render(Options{Server: server, Domain: "photos.example.net", Upstream: "[::1]:9100"})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"photos.example.net", "[::1]:9100", "_SECURE_COOKIES=true", proxyEnvironment} {
				if !strings.Contains(config, want) {
					t.Errorf("missing %q in generated %s config", want, server)
				}
			}
			if strings.Contains(config, exampleDomain) || strings.Contains(config, defaultUpstream) {
				t.Fatal("generated config contains the original example's host or upstream")
			}
			if server != "caddy" {
				if !strings.Contains(config, `"/etc/letsencrypt/live/photos.example.net/fullchain.pem"`) {
					t.Fatal("default certificate path does not follow the domain")
				}
				custom, err := Render(Options{Server: server, Domain: "photos.example.net", Certificate: "/custom certs/fullchain.pem", Key: "/custom certs/key.pem"})
				if err != nil || !strings.Contains(custom, `"/custom certs/fullchain.pem"`) || !strings.Contains(custom, `"/custom certs/key.pem"`) {
					t.Fatalf("custom TLS paths were not quoted: %v\n%s", err, custom)
				}
			}
		})
	}
}

func TestProxyConfigRejectsInjectionAndInvalidAddresses(t *testing.T) {
	base := Options{Server: "nginx", Domain: "photos.example.net", Upstream: defaultUpstream}
	var cases []Options
	for _, domain := range []string{"", "https://photos.example.net", "bad.example/path", "*.example.net", "photos.example.net:443", "bad\nserver{}", "bad;include", "bad$host", "bad..example", "-bad.example", "bad_.example", strings.Repeat("a", 64) + ".example"} {
		value := base
		value.Domain = domain
		cases = append(cases, value)
	}
	for _, upstream := range []string{":8080", "0.0.0.0:8080", "192.0.2.1:8080", "http://127.0.0.1:8080", "127.0.0.1:0", "127.0.0.1:65536", "localhost:abc", "localhost:8080;", "localhost:8080\n", "localhost:+8080"} {
		value := base
		value.Upstream = upstream
		cases = append(cases, value)
	}
	for _, path := range []string{"relative.pem", "/certs/", "/certs/\ninclude", "/certs/$variable", "/certs/\".pem", "/certs/a;include", "/certs/\\.pem"} {
		value := base
		value.Certificate, value.Key = path, "/certs/key.pem"
		cases = append(cases, value)
	}
	cases = append(cases, Options{Server: "unknown", Domain: base.Domain}, Options{Server: "nginx", Domain: base.Domain, Certificate: "/cert.pem"}, Options{Server: "caddy", Domain: base.Domain, Certificate: "/cert.pem", Key: "/key.pem"})
	for _, value := range cases {
		if output, err := Render(value); err == nil || output != "" {
			t.Errorf("invalid options accepted: %+v => %q, %v", value, output, err)
		}
	}
}

func TestProxyConfigCLI(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"caddy", "--help"}, {"nginx", "--help"}, {"apache", "--help"}} {
		var output bytes.Buffer
		if err := Run(args, &output); err != nil || !strings.Contains(output.String(), "Usage:") {
			t.Fatalf("help %v: %v, %s", args, err, &output)
		}
	}
	var output bytes.Buffer
	if err := Run([]string{"caddy", "--domain", "photos.example.net"}, &output); err != nil || !strings.Contains(output.String(), "reverse_proxy "+defaultUpstream) {
		t.Fatalf("render: %v, %s", err, &output)
	}
	for _, args := range [][]string{nil, {"unknown"}, {"caddy"}, {"caddy", "--domain", "photos.example.net", "extra"}} {
		output.Reset()
		if err := Run(args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("bad arguments emitted config: %v => %v, %s", args, err, &output)
		}
	}
	sentinel := errors.New("output closed")
	if err := Run([]string{"caddy", "--domain", "photos.example.net"}, brokenWriter{sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("output error = %v", err)
	}
}

type brokenWriter struct{ err error }

func (w brokenWriter) Write([]byte) (int, error) { return 0, w.err }

func TestGeneratedCaddyConfigurationAdapts(t *testing.T) {
	binary := os.Getenv("CADDY_BINARY")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("caddy")
		if err != nil {
			t.Skip("install Caddy to validate its generated configuration")
		}
	}
	config, err := Render(Options{Server: "caddy", Domain: "photos.example.net", Upstream: "[::1]:9100"})
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
