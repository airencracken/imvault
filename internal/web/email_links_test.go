// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/config"
)

// postForgotWithHost asks for a reset the way a forged request would: with a
// Host header naming somewhere else.
func postForgotWithHost(t *testing.T, h *harness, identifier, host string) {
	t.Helper()

	s := h.newSession(t)
	form := url.Values{"csrf_token": {s.csrf}, "identifier": {identifier}}
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/forgot", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = host
	// The jar matches on the forged host, so the token travels by hand.
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: s.csrf})
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("forgot = %d", resp.StatusCode)
	}
	h.srv.waitBackground()
}

// A reset link is a credential delivered to somebody else, so it must point at
// the configured address whatever the request claimed its host was.
func TestResetLinksIgnoreTheRequestHost(t *testing.T) {
	h := newHarnessFull(t, func(cfg *config.Config) {
		cfg.BaseURL = "https://photos.example.net"
	}, mailDirect)
	h.seedUser("victim")

	postForgotWithHost(t, h, "victim", "attacker.example")

	msg := h.mailer.last(t)
	if strings.Contains(msg.Body, "attacker.example") {
		t.Fatalf("the reset link follows the forged host:\n%s", msg.Body)
	}
	if !strings.Contains(msg.Body, "https://photos.example.net/reset/") {
		t.Errorf("the reset link does not use the configured address:\n%s", msg.Body)
	}
}

// Without a configured address there is nothing trustworthy to build a link
// from, so nothing is sent rather than something forged.
func TestEmailedLinksNeedABaseURL(t *testing.T) {
	h := newHarnessFull(t, nil, mailDirect)
	h.srv.cfg.BaseURL = ""
	h.seedUser("victim")

	postForgotWithHost(t, h, "victim", "attacker.example")

	for _, msg := range h.mailer.sent() {
		if strings.Contains(msg.Body, "/reset/") {
			t.Fatalf("a reset link was mailed without a configured address:\n%s", msg.Body)
		}
	}
	if _, err := h.srv.emailLink("/reset/x"); err == nil {
		t.Error("emailLink built a link with no base URL")
	}
}
