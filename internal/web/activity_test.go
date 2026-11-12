// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"imvault/internal/models"
	"io"
	"strings"
	"testing"
	"time"
)

func TestActivityCountsAuthorizedOpensAndSuccessfulLogins(t *testing.T) {
	h := newHarness(t)
	owner := h.provisionAdmin("boss")
	id := uploadPlain(t, h, map[string]string{"visibility": "public"})
	for i := 0; i < 2; i++ {
		if response, _ := h.get("/f/" + id); response.StatusCode != 200 {
			t.Fatal(response.StatusCode)
		}
	}
	h.get("/f/" + id + "/thumb")
	h.get("/gallery")
	h.get("/f/does-not-exist")
	result, err := h.store.Analytics(t.Context(), time.Now())
	if err != nil || result.Logins != 1 || result.Views != 1 {
		t.Fatal(result, err)
	}
	if err := h.store.CreateSession(t.Context(), "rotated-for-activity-test", owner.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	result, err = h.store.Analytics(t.Context(), time.Now())
	if err != nil || result.Logins != 1 {
		t.Fatal("session rotation counted", result, err)
	}
	mod := h.seedUser("moderator")
	if err := h.store.SetUserRole(t.Context(), mod.ID, models.RoleModerator); err != nil {
		t.Fatal(err)
	}
	client := h.signIn(t, mod.ID)
	response, err := client.Get(h.server.URL + "/admin/metrics?role=admin")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	mustClose(t, response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 403 || strings.Contains(string(body), "Recent sign-ins") {
		t.Fatal("moderator read analytics", response.StatusCode)
	}
	if response, body := h.get("/admin/metrics"); response.StatusCode != 200 || !strings.Contains(body, "comfy-admin-dashboard") {
		t.Fatal(response.StatusCode, body)
	}
}
