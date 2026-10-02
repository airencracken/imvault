// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/airencracken/comfylib/smtp"

	"imvault/internal/config"
)

// Without a relay the sender refuses mail and logs none of it. A reset link in
// the log would be as good as the password it replaces, and the old disabled
// sender logged every body at info level.
func TestWithoutARelayMailIsRefusedAndNeverLogged(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sender, err := mailSender(&config.Config{SMTPFrom: "Imvault <no-reply@localhost>"}, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	if sender.Enabled() {
		t.Fatal("a sender without a relay reports itself enabled")
	}
	msg := smtp.Message{To: "alice@example.org", Subject: "Reset subject 7f3a", Body: "https://img.example.com/reset/secret-token-9c1e"}
	if err := sender.Send(context.Background(), msg); !errors.Is(err, smtp.ErrDisabled) {
		t.Fatalf("Send without a relay = %v, want ErrDisabled", err)
	}
	for _, leaked := range []string{"secret-token-9c1e", "Reset subject 7f3a", "alice@example.org"} {
		if strings.Contains(logs.String(), leaked) {
			t.Errorf("the log carries %q:\n%s", leaked, logs.String())
		}
	}
}

func TestAConfiguredRelayIsQueuedAndAnInvalidOneStopsStartup(t *testing.T) {
	valid := config.Config{SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPFrom: "<no-reply@example.com>", SMTPTLS: smtp.TLSStartTLS}
	logger := slog.New(slog.DiscardHandler)
	sender, err := mailSender(&valid, nil, logger)
	if err != nil || !sender.Enabled() {
		t.Fatalf("a valid relay was refused: %v", err)
	}
	for name, change := range map[string]func(*config.Config){
		"host with a space": func(c *config.Config) { c.SMTPHost = "smtp example.com" },
		"host with CRLF":    func(c *config.Config) { c.SMTPHost = "smtp.example.com\r\nRCPT" },
		"port zero":         func(c *config.Config) { c.SMTPPort = 0 },
		"unknown mode":      func(c *config.Config) { c.SMTPTLS = "tls" },
		"malformed sender":  func(c *config.Config) { c.SMTPFrom = "no-reply@example.com\r\nBcc: x@example.org" },
	} {
		cfg := valid
		change(&cfg)
		if _, err := mailSender(&cfg, nil, logger); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
