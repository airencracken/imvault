// SPDX-License-Identifier: AGPL-3.0-or-later
package maintenance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/comfylib/autobackup"
)

func TestAutomaticBackupCopiesVerifiedMediaAndBoundsStaging(t *testing.T) {
	f := newFixture(t)
	dest := filepath.Join(t.TempDir(), "snapshot")
	if err := BackupLimited(t.Context(), f.cfg, f.store, f.objects, dest, 10<<20, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := Restore(t.Context(), dest, filepath.Join(t.TempDir(), "restored"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := BackupLimited(t.Context(), f.cfg, f.store, f.objects, filepath.Join(t.TempDir(), "small"), 1, io.Discard); err == nil {
		t.Fatal("database cap ignored")
	}
	var pages, size int64
	if err := f.store.DB().QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow("PRAGMA page_size").Scan(&size); err != nil {
		t.Fatal(err)
	}
	limited := filepath.Join(t.TempDir(), "limited")
	if err := BackupLimited(t.Context(), f.cfg, f.store, f.objects, limited, pages*size, io.Discard); err == nil {
		t.Fatal("key/media/manifest copy cap ignored")
	}
	if _, err := os.Stat(limited); !os.IsNotExist(err) {
		t.Fatal("partial snapshot published", err)
	}
	for _, limit := range []int64{0, -1} {
		if err := BackupLimited(t.Context(), f.cfg, f.store, f.objects, limited, limit, io.Discard); err == nil {
			t.Fatal(limit)
		}
	}
	// Budget enforcement is on actual streamed bytes, including unknown-size
	// derived objects and a source that grows after Stat.
	budget := int64(3)
	backend := &copyBudgetBackend{Backend: f.objects, remaining: &budget}
	if _, err := backend.Save(t.Context(), "test/exceeds", bytes.NewReader([]byte("four"))); err == nil {
		t.Fatal("streaming cap ignored")
	}
	if _, err := f.objects.Stat(t.Context(), "test/exceeds"); err == nil {
		t.Fatal("oversized object was published")
	}
	t.Setenv("PATH", t.TempDir())
	cfg, err := autobackup.Load("imvault", t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	out, err := autobackup.Create(t.Context(), cfg, func(ctx context.Context, path string, cap int64) error {
		return BackupLimited(ctx, f.cfg, f.store, f.objects, path, cap, io.Discard)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "snapshot.tar.gz")); err != nil {
		t.Fatal(err)
	}
	extracted := unpackAutomaticTest(t, filepath.Join(out, "snapshot.tar.gz"))
	if err := Restore(t.Context(), extracted, filepath.Join(t.TempDir(), "compressed-restored"), io.Discard); err != nil {
		t.Fatal("compressed backup cannot restore", err)
	}
}

func unpackAutomaticTest(t *testing.T, archive string) string {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := gz.Close(); err != nil {
			t.Error(err)
		}
	}()
	dest := t.TempDir()
	root, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsLocal(h.Name) {
			t.Fatal("unsafe generated archive name", h.Name)
		}
		if h.Typeflag == tar.TypeDir {
			if err := root.MkdirAll(h.Name, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if h.Typeflag != tar.TypeReg {
			t.Fatal("unsafe generated archive type", h.Typeflag)
		}
		if err := root.MkdirAll(filepath.Dir(h.Name), 0o700); err != nil {
			t.Fatal(err)
		}
		file, err := root.OpenFile(h.Name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(file, r)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatal(copyErr, closeErr)
		}
	}
	if _, err := io.Copy(io.Discard, gz); err != nil {
		t.Fatal("gzip checksum", err)
	}
	return dest
}
