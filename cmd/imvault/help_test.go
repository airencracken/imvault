package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIHelpAndProxyConfigDoNotTouchData(t *testing.T) {
	data := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("IMVAULT_DATA_DIR", data)
	t.Setenv("IMVAULT_STORAGE", "invalid-on-purpose")
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"}, {"serve", "--help"}, {"help", "serve"},
		{"create-admin", "--help"}, {"help", "create-admin"}, {"backup", "--help"},
		{"restore", "--help"}, {"refresh-metadata", "--help"}, {"migrate-storage", "--help"},
		{"rebuild-thumbnails", "--help"}, {"proxy-config", "--help"}, {"help", "proxy-config"},
		{"proxy-config", "nginx", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			if err := runCommand(args, strings.NewReader(""), &output); err != nil || !strings.Contains(output.String(), "Usage: imvault") {
				t.Fatalf("help: %v\n%s", err, &output)
			}
		})
	}
	for _, server := range []string{"caddy", "nginx", "apache"} {
		var output bytes.Buffer
		if err := runCommand([]string{"proxy-config", server, "--domain", "img.internetrelay.chat", "--upstream", "127.0.0.1:9100"}, strings.NewReader(""), &output); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "img.internetrelay.chat") || !strings.Contains(output.String(), "127.0.0.1:9100") {
			t.Fatalf("wrong proxy config: %s", &output)
		}
	}
	for _, args := range [][]string{{"wat"}, {"help", "wat"}, {"serve", "extra"}, {"--help", "extra"}, {"create-admin"}} {
		if err := runCommand(args, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Fatalf("invalid arguments succeeded: %v", args)
		}
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("help/config generation touched data: %v", err)
	}
}

func TestHelpExplainsDeployment(t *testing.T) {
	var output bytes.Buffer
	if err := runCommand([]string{"--help"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"IMVAULT_ADDR", "IMVAULT_DATA_DIR", "IMVAULT_BASE_URL", "IMVAULT_SECURE_COOKIES", "/etc/conf.d/imvault", "logrotate", "journald", "proxy-config", "password-prompt", "password-stdin"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("help omits %s", want)
		}
	}
}
