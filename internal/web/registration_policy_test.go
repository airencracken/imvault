// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/config"
	"imvault/internal/models"
)

func TestEmptyInstanceHonorsRegistrationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*config.Config)
		message   string
	}{
		{"closed", func(c *config.Config) { c.AllowSignup = false }, "not taking new accounts"},
		{"invitation only", func(c *config.Config) { c.InviteOnly = true }, "by invitation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessWith(t, tc.configure)
			_, page := h.get("/register")
			if !strings.Contains(page, tc.message) {
				t.Error("registration page does not reflect the policy")
			}
			resp, _ := h.postForm("/register", url.Values{
				"username": {"stranger"}, "password": {testPassword},
			})
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("first registration = %d, want 403", resp.StatusCode)
			}
			count, err := h.store.CountUsers(t.Context())
			if err != nil || count != 0 {
				t.Fatalf("users after rejected registration = %d, err %v", count, err)
			}
		})
	}
}

func TestFirstPublicRegistrationIsOrdinaryMember(t *testing.T) {
	h := newHarness(t)
	h.get("/register")
	resp, _ := h.postForm("/register", url.Values{
		"username": {"stranger"}, "password": {testPassword}, "role": {"admin"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("registration = %d, want 303", resp.StatusCode)
	}
	user, err := h.store.UserByUsername(t.Context(), "stranger")
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != models.RoleMember {
		t.Errorf("first public account has role %q, want member", user.Role)
	}
	if resp, _ := h.get("/admin"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("first public account can access admin: %d", resp.StatusCode)
	}
}
