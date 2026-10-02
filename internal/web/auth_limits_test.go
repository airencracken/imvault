// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"imvault/internal/config"
)

// Every route that checks a credential, mints an account or sends mail for an
// anonymous caller shares the sign-in budget.
func TestCredentialRoutesShareTheSignInBudget(t *testing.T) {
	signedOut := []string{"/forgot", "/register", "/reset/not-a-token", "/login"}
	signedIn := []string{"/settings/password", "/settings/email", "/settings/2fa/disable",
		"/settings/2fa/recovery", "/settings/account/delete"}

	for _, path := range append(append([]string{}, signedOut...), signedIn...) {
		t.Run(path, func(t *testing.T) {
			h := newHarnessFull(t, func(c *config.Config) {
				c.LoginRatePerHour = 1
				c.LoginBurst = 2
			}, mailDirect)
			user := h.seedUser("alice")
			s := h.newSession(t)
			for _, p := range signedIn {
				if p == path {
					s = h.sessionFor(t, user.ID)
				}
			}
			var last int
			for i := 0; i < 3; i++ {
				resp, _ := s.post(path, url.Values{"identifier": {"alice"}, "password": {"wrong"}, "current_password": {"wrong"}})
				last = resp.StatusCode
			}
			if last != http.StatusTooManyRequests {
				t.Errorf("third attempt = %d, want 429", last)
			}
		})
	}
}

// Asking for a reset answers without waiting on the relay, so the time a
// response takes does not say whether the account exists.
func TestForgotDoesNotWaitForTheRelay(t *testing.T) {
	h := newMailHarness(t)
	h.seedUser("alice")
	h.mailer.gate = make(chan struct{})
	defer func() {
		close(h.mailer.gate)
		h.srv.waitBackground()
	}()

	s := h.newSession(t)
	form := url.Values{"csrf_token": {s.token()}, "identifier": {"alice"}}
	done := make(chan int, 1)
	go func() {
		resp, err := s.client.PostForm(h.server.URL+"/forgot", form)
		if err != nil {
			done <- 0
			return
		}
		_ = resp.Body.Close()
		done <- resp.StatusCode
	}()
	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Errorf("forgot = %d", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("asking for a reset waited for the mail relay")
	}
}
