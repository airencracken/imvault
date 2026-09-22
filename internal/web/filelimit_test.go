// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strings"
	"testing"

	"imvault/internal/config"
)

func TestPerAccountFileLimitOverridesTheInstanceDefault(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	alice := h.seedUser("alice")
	if err := h.store.SetPassword(t.Context(), alice.ID,
		mustHashPassword(t, testPassword)); err != nil {
		t.Fatal(err)
	}
	me := h.sessionFor(t, alice.ID)

	image := pngFixture(t, 40, 40)

	// Under the instance default, so this upload is accepted.
	if resp, body := me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "ok.png", data: image},
	}); resp.StatusCode != http.StatusOK || strings.Contains(body, "larger than") {
		t.Fatalf("the upload should have been accepted: %s", truncate(body))
	}

	// Cap the account at half the fixture's size, derived from the fixture
	// rather than guessed at: a PNG this small is only a few hundred bytes.
	limit := int64(len(image)) / 2
	if limit <= 0 {
		t.Fatalf("the fixture is %d bytes, too small for this test", len(image))
	}
	if err := h.store.SetUserFileLimit(t.Context(), alice.ID, limit); err != nil {
		t.Fatal(err)
	}

	resp, body := me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "too-big.png", data: image},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d, want a 200 carrying the error", resp.StatusCode)
	}
	if !strings.Contains(body, "larger than") {
		t.Errorf("the account's own file limit was not applied: %s", truncate(body))
	}
}

func TestPerAccountFileLimitCanRaiseTheCeiling(t *testing.T) {
	// The instance default is tiny, and the account is allowed more.
	// A 64-byte ceiling, which every generated fixture comfortably exceeds.
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.MaxUploadBytes = 64
		cfg.MaxVideoBytes = 64
	})
	h.provisionAdmin("boss")

	alice := h.seedUser("alice")
	if err := h.store.SetPassword(t.Context(), alice.ID,
		mustHashPassword(t, testPassword)); err != nil {
		t.Fatal(err)
	}
	me := h.sessionFor(t, alice.ID)

	image := pngFixture(t, 64, 64)
	if int64(len(image)) <= 64 {
		t.Fatalf("the fixture is %d bytes, too small to prove anything", len(image))
	}

	// Refused under the instance default.
	_, body := me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "big.png", data: image},
	})
	if !strings.Contains(body, "larger than") {
		t.Fatalf("the instance default was not applied: %s", truncate(body))
	}

	// An override replaces it, so the same upload now fits. The request itself
	// also has to be allowed through, which is what the request-level bound is
	// for.
	if err := h.store.SetUserFileLimit(t.Context(), alice.ID, 1<<20); err != nil {
		t.Fatal(err)
	}

	resp, body := me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "big.png", data: image},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d, want 200", resp.StatusCode)
	}
	if strings.Contains(body, "larger than") || strings.Contains(body, "exceeded") {
		t.Errorf("the raised limit was not honoured: %s", truncate(body))
	}
}

func TestUploadPageReportsTheAccountsOwnLimit(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	alice := h.seedUser("alice")
	if err := h.store.SetPassword(t.Context(), alice.ID,
		mustHashPassword(t, testPassword)); err != nil {
		t.Fatal(err)
	}
	me := h.sessionFor(t, alice.ID)

	// The instance default from the harness config is 8 MiB.
	_, page := me.get("/upload")
	if !strings.Contains(page, "8 MiB") {
		t.Errorf("the uploader does not state the instance limit: %s", truncate(page))
	}

	if err := h.store.SetUserFileLimit(t.Context(), alice.ID, 3<<20); err != nil {
		t.Fatal(err)
	}

	_, page = me.get("/upload")
	if !strings.Contains(page, "3 MiB") {
		t.Errorf("the uploader still reports the instance limit: %s", truncate(page))
	}
}
