// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"imvault/internal/config"
)

// tightLimit is a budget small enough to exhaust in a test.
func tightLimit(cfg *config.Config) {
	cfg.UploadRatePerHour = 60 // one per minute, so the bucket stays empty
	cfg.UploadBurst = 3
}

func TestUploadRateLimitIsPerAccount(t *testing.T) {
	h := newHarnessWith(t, tightLimit)

	alice := h.seedUser("alice")
	bob := h.seedUser("bob")
	aliceKey := h.seedKey(alice.ID, "alice", nil)
	bobKey := h.seedKey(bob.ID, "bob", nil)

	// The burst allowance is spent.
	for i := 0; i < 3; i++ {
		resp, raw := h.apiUpload(aliceKey, nil, map[string][]byte{
			"pic.png": pngFixture(t, 20, 20),
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("upload %d = %d, want 201 (body: %s)", i+1, resp.StatusCode, truncate(string(raw)))
		}
	}

	// The next one is refused, with a usable Retry-After.
	resp, raw := h.apiUpload(aliceKey, nil, map[string][]byte{
		"pic.png": pngFixture(t, 20, 20),
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("upload past the budget = %d, want 429 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}

	retryAfter, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || retryAfter < 1 {
		t.Errorf("Retry-After = %q, want a positive number of seconds", resp.Header.Get("Retry-After"))
	}

	var body map[string]string
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("429 body is not JSON: %s", truncate(string(raw)))
	}
	if body["error"] == "" {
		t.Error("429 body does not explain the problem")
	}

	// Another account has its own budget: throttling one must not affect
	// anybody else.
	if resp, raw := h.apiUpload(bobKey, nil, map[string][]byte{
		"pic.png": pngFixture(t, 20, 20),
	}); resp.StatusCode != http.StatusCreated {
		t.Errorf("bob's first upload = %d, want 201 (body: %s)", resp.StatusCode, truncate(string(raw)))
	}

	// A read endpoint is never limited.
	if resp, _ := h.apiJSON(http.MethodGet, "/api/v1/files", aliceKey, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("listing after being throttled = %d, want 200", resp.StatusCode)
	}
}

func TestRateLimitCoversTheWebUploaderToo(t *testing.T) {
	h := newHarnessWith(t, tightLimit)
	h.registerForm("marcus")

	// The browser uploader shares the same per-account budget as the API.
	for i := 0; i < 3; i++ {
		resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
			{name: "pic.png", data: pngFixture(t, 20, 20)},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("web upload %d = %d, want 200 (body: %s)", i+1, resp.StatusCode, truncate(body))
		}
	}

	resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "pic.png", data: pngFixture(t, 20, 20)},
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("web upload past the budget = %d, want 429 (body: %s)", resp.StatusCode, truncate(body))
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 response is missing Retry-After")
	}
}

func TestRateLimitKeysAnonymousUploadsByAddress(t *testing.T) {
	h := newHarnessWith(t, tightLimit)

	// Anonymous uploads have no account, so they are bucketed by address. Every
	// request in this test comes from the same address, and they must therefore
	// share one budget.
	h.get("/upload")

	for i := 0; i < 3; i++ {
		resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
			{name: "anon.png", data: pngFixture(t, 20, 20)},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("anonymous upload %d = %d, want 200 (body: %s)", i+1, resp.StatusCode, truncate(body))
		}
	}

	resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "anon.png", data: pngFixture(t, 20, 20)},
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("anonymous upload past the budget = %d, want 429 (body: %s)", resp.StatusCode, truncate(body))
	}
}

func TestClientIPIgnoresProxyHeadersByDefault(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) { cfg.TrustProxyHeaders = false })

	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A spoofed header must not change the bucket, or the limit is trivially
	// bypassed.
	req.Header.Set("X-Forwarded-For", "203.0.113.9")

	direct := h.srv.clientIP(req)
	if direct == "203.0.113.9" {
		t.Error("proxy headers were trusted without being enabled")
	}

	trusting := newHarnessWith(t, func(cfg *config.Config) { cfg.TrustProxyHeaders = true })
	if got := trusting.srv.clientIP(req); got != "203.0.113.9" {
		t.Errorf("with TrustProxyHeaders, clientIP = %q, want the forwarded address", got)
	}
}
