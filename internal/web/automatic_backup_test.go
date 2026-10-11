// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"context"
	"github.com/airencracken/comfylib/autobackup"
	"strings"
	"testing"
)

func TestBackupStatusIsAdminOnly(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("keeper")
	cfg, err := autobackup.Load("imvault", t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	h.srv.Backups, err = autobackup.New(cfg, func(context.Context, string, int64) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, body := h.get("/admin/settings")
	if resp.StatusCode != 200 || !strings.Contains(body, cfg.Directory) || !strings.Contains(body, "No automatic backup has succeeded") {
		t.Fatal(resp.StatusCode, body)
	}
	member := h.sessionFor(t, h.seedUser("member").ID)
	resp, body = member.get("/admin/settings")
	if resp.StatusCode != 403 || strings.Contains(body, cfg.Directory) {
		t.Fatal(resp.StatusCode, body)
	}
	for _, path := range []string{"/healthz", "/about"} {
		_, body = member.get(path)
		if strings.Contains(body, cfg.Directory) {
			t.Fatal("private backup status leaked", path)
		}
	}
}
