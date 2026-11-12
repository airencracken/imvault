// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOwnerMetricsTotalsPermissionsAndFailure(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	regular := h.seedUser("regular")
	member := h.signIn(t, regular.ID)
	metrics, err := h.store.Metrics(t.Context(), time.Now())
	if err != nil || len(metrics) < 6 || metrics[0].Value != 2 {
		t.Fatal(metrics, err)
	}
	guest := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/admin/metrics", "/admin/metrics?owner=true&role=admin"} {
		resp, body := h.get(path)
		if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(body, "Instance overview") {
			t.Fatal(resp.StatusCode, body)
		}
		for _, client := range []*http.Client{member, guest} {
			res, err := client.Get(h.server.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(res.Body)
			mustClose(t, res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != 303 && res.StatusCode != 403 {
				t.Fatal("metrics authorization", res.StatusCode)
			}
			if strings.Contains(string(content), "Instance overview") {
				t.Fatal("metrics leaked")
			}
		}
	}
	if _, err = h.store.DB().Exec("ALTER TABLE blobs RENAME TO unavailable_blobs"); err != nil {
		t.Fatal(err)
	}
	if resp, body := h.get("/admin/metrics"); resp.StatusCode != 500 || strings.Contains(body, "Instance overview") {
		t.Fatal("partial metrics published", resp.StatusCode)
	}
}
