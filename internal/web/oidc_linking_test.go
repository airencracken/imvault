// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strings"
	"testing"

	"imvault/internal/models"
	"imvault/internal/store"
)

// An address somebody typed in here and never confirmed must not decide whose
// account a provider identity lands in. Otherwise registering with a stranger's
// address is a way to be handed their first provider sign-in.
func TestOIDCDoesNotLinkToAnUnconfirmedLocalAddress(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.provisionAdmin("boss")

	squatter, err := h.store.CreateUser(t.Context(), store.NewUser{
		Username:     "mallory",
		Email:        "victim@example.org",
		PasswordHash: mustHashPassword(t, testPassword),
		Role:         models.RoleMember,
	})
	if err != nil {
		t.Fatal(err)
	}

	idp.setIdentity("victim-subject", "victim@example.org", true, "victim")
	client := h.newSession(t)
	resp := h.callback(t, idp, client.client, h.authorize(t, client.client, ""))

	if resp.StatusCode != http.StatusSeeOther || !strings.HasSuffix(resp.Header.Get("Location"), "/auth/oidc/complete") {
		t.Fatalf("an unconfirmed local address skipped registration: %d -> %q",
			resp.StatusCode, resp.Header.Get("Location"))
	}
	if identity, err := h.store.IdentityBySubject(t.Context(), idp.server.URL, "victim-subject"); err == nil {
		t.Fatalf("the identity was linked to account %d (squatter is %d)", identity.UserID, squatter.ID)
	}
	if client.signedIn() {
		t.Error("the provider sign-in was handed a session before registering")
	}
}

// One confirmed account and any number of unconfirmed ones sharing the address
// is not ambiguous: only the confirmed one has proved anything.
func TestOIDCLinksToTheOnlyConfirmedAddress(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.provisionAdmin("boss")

	owner := h.seedUser("owner")
	if err := h.store.SetEmail(t.Context(), owner.ID, "shared@example.org", true); err != nil {
		t.Fatal(err)
	}
	squatter := h.seedUser("squatter")
	if err := h.store.SetEmail(t.Context(), squatter.ID, "shared@example.org", false); err != nil {
		t.Fatal(err)
	}

	idp.setIdentity("owner-subject", "shared@example.org", true, "owner")
	client := h.newSession(t)
	resp := h.callback(t, idp, client.client, h.authorize(t, client.client, ""))
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/recent" {
		t.Fatalf("callback = %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	identity, err := h.store.IdentityBySubject(t.Context(), idp.server.URL, "owner-subject")
	if err != nil || identity.UserID != owner.ID {
		t.Fatalf("linked to %+v (%v), want account %d", identity, err, owner.ID)
	}
}
