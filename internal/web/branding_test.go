// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"image/png"
	"net/http"
	"strings"
	"testing"
)

func TestDefaultBrandAssetsAreServed(t *testing.T) {
	h := newHarness(t)
	_, page := h.get("/")
	for path, size := range map[string]int{
		"/static/img/mascot.png":     256,
		"/static/img/favicon-32.png": 32,
		"/static/img/favicon-16.png": 16,
	} {
		if !strings.Contains(page, path) {
			t.Errorf("default layout does not reference %s", path)
		}
		resp, err := h.client.Get(h.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, decodeErr := png.DecodeConfig(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || decodeErr != nil {
			t.Fatalf("asset %s: status=%d, decode=%v", path, resp.StatusCode, decodeErr)
		}
		if cfg.Width != size || cfg.Height != size {
			t.Errorf("asset %s: got %dx%d, want %dx%d", path, cfg.Width, cfg.Height, size, size)
		}
	}
}
