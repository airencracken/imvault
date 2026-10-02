// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"

	"imvault/internal/storage"
)

// openFailsAfter is a backend whose reads start failing after a number of
// successful opens, standing in for storage that goes away mid-export. The
// server's background workers may open objects while the export runs, so the
// count is shared and guarded.
type openFailsAfter struct {
	storage.Backend
	mu        sync.Mutex
	remaining int
}

func (o *openFailsAfter) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	o.mu.Lock()
	if o.remaining <= 0 {
		o.mu.Unlock()
		return nil, errors.New("storage went away")
	}
	o.remaining--
	o.mu.Unlock()
	return o.Backend.Open(ctx, key)
}

// An export that fails part-way must not arrive as a valid archive that is
// quietly missing files.
func TestAFailedExportIsNotAValidArchive(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	owner := h.sessionFor(t, user.ID)
	for _, name := range []string{"one.png", "two.png", "three.png"} {
		uploadAs(t, owner, name, "private")
	}
	h.srv.objects = &openFailsAfter{Backend: h.srv.objects, remaining: 1}

	resp, err := owner.client.Get(h.server.URL + "/settings/account/export")
	if err != nil {
		// Failing before anything was flushed drops the connection outright,
		// which is a failed download, as it should be.
		return
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export = %d", resp.StatusCode)
	}
	if _, zipErr := zip.NewReader(bytes.NewReader(body), int64(len(body))); zipErr == nil && readErr == nil && closeErr == nil {
		t.Fatal("a failed export arrived as a complete, valid archive")
	}
}
