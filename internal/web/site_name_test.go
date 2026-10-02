// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/url"
	"strings"
	"testing"

	"imvault/internal/config"
)

// What people read is in the instance's own name, not the software's.
func TestUserFacingTextUsesTheSiteName(t *testing.T) {
	h := newHarnessFull(t, func(c *config.Config) { c.Name = "Family Photos" }, mailDirect)
	h.seedUser("alice")
	postForgotWithHost(t, h, "alice", "")

	msg := h.mailer.last(t)
	if msg.Subject != "Reset your Family Photos password" || !strings.Contains(msg.Body, "your Family Photos account") {
		t.Errorf("the reset mail does not use the site name: %q\n%s", msg.Subject, msg.Body)
	}
	if strings.Contains(strings.ToLower(msg.Subject+msg.Body), "imvault") {
		t.Error("the reset mail names the software instead of the site")
	}

	s := h.newSession(t)
	resp, _ := s.post("/register", url.Values{"username": {"bob"}, "password": {testPassword}})
	_, page := s.get(resp.Header.Get("Location"))
	if !strings.Contains(page, "Welcome to Family Photos.") {
		t.Error("the welcome notice does not use the site name")
	}

	if got := exportFilename("Family Photos", "bob"); !strings.HasPrefix(got, "family-photos-bob-") {
		t.Errorf("export filename = %q", got)
	}
}

// With no name configured the product name is written as a name.
func TestTheDefaultSiteNameIsCapitalised(t *testing.T) {
	h := newHarnessWith(t, func(c *config.Config) { c.Name = "" })
	if got := h.srv.branding().SiteName; got != "Imvault" {
		t.Errorf("default site name = %q, want Imvault", got)
	}
	if !strings.HasPrefix(h.srv.branding().WelcomeText, "Imvault is ") {
		t.Errorf("default welcome text = %q", h.srv.branding().WelcomeText)
	}
}
