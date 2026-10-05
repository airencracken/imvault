// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"imvault/internal/config"
	"imvault/internal/instance"
	"imvault/internal/maintenance"
	"imvault/internal/storage"
	"imvault/internal/store"
)

type pausedBackupSource struct {
	storage.Backend
	once            sync.Once
	entered, resume chan struct{}
}

func (p *pausedBackupSource) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	p.once.Do(func() {
		close(p.entered)
		select {
		case <-p.resume:
		case <-ctx.Done():
		}
	})
	return p.Backend.Open(ctx, key)
}

func TestLiveBackupRetainsSnapshotObjectsDuringConcurrentDeletion(t *testing.T) {
	h := newHarnessWith(t, func(cfg *config.Config) { cfg.SecretKeyFile = filepath.Join(cfg.DataDir, "secret.key") })
	user := h.seedUser("alice")
	owner := h.sessionFor(t, user.ID)
	id := uploadAs(t, owner, "keep.png", "private")
	server, err := instance.AcquireServer(h.srv.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer mustClose(t, server)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	source := &pausedBackupSource{Backend: h.srv.objects, entered: make(chan struct{}), resume: make(chan struct{})}
	defer func() {
		source.once.Do(func() { close(source.entered) })
		select {
		case <-source.resume:
		default:
			close(source.resume)
		}
	}()
	backup := filepath.Join(t.TempDir(), "snapshot")
	completed := make(chan error, 1)
	go func() { completed <- maintenance.Backup(ctx, h.srv.cfg, h.store, source, backup, io.Discard) }()
	select {
	case <-source.entered:
	case err := <-completed:
		t.Fatal("backup did not reach object copy", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	deleted := make(chan int, 1)
	go func() { response, _ := owner.post("/f/"+id+"/delete", url.Values{}); deleted <- response.StatusCode }()
	for {
		_, err := h.store.FileByID(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("live deletion did not update database", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case code := <-deleted:
		t.Fatalf("object deletion bypassed backup lock: %d", code)
	default:
	}
	guest := h.newSession(t)
	response, _ := guest.get("/about")
	if response.StatusCode != 200 {
		t.Fatal("backup blocked normal browsing")
	}
	close(source.resume)
	if err := <-completed; err != nil {
		t.Fatal("live backup failed", err)
	}
	select {
	case code := <-deleted:
		if code != 303 {
			t.Fatalf("deletion after snapshot: %d", code)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := maintenance.Restore(ctx, backup, restored, io.Discard); err != nil {
		t.Fatal("live snapshot cannot be restored", err)
	}
}

func TestBrowsingDoesNotTrackViews(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	owner := h.sessionFor(t, user.ID)
	id := uploadAs(t, owner, "photo.png", "public")
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	initial := file.Views
	_, page := owner.get("/f/" + id)
	if strings.Contains(page, "<dt>Views</dt>") {
		t.Fatal("engagement counter remains visible")
	}
	guest := h.newSession(t)
	for i := 0; i < 3; i++ {
		req, err := http.NewRequest("GET", h.server.URL+"/f/"+id+"/raw", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Range", "bytes=0-7")
		response, err := guest.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		mustClose(t, response.Body)
		if response.StatusCode != 206 {
			t.Fatalf("range=%d", response.StatusCode)
		}
	}
	file, err = h.store.FileByID(t.Context(), id)
	if err != nil || file.Views != initial {
		t.Fatal("browsing still tracks views", err)
	}
}
