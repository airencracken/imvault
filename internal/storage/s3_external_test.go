// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"imvault/internal/config"
)

// Opt-in provider verification. An isolated random prefix confines writes and
// cleanup to objects created by this test; no pre-existing keys are listed.
func TestS3ExternalEndpoint(t *testing.T) {
	if os.Getenv("IMVAULT_TEST_S3_BUCKET") == "" {
		t.Skip("set IMVAULT_TEST_S3_BUCKET and provider settings to test a real endpoint")
	}
	t.Setenv("IMVAULT_TEST_STORAGE", "s3")
	cfg, err := config.LoadStorage("IMVAULT_TEST_", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Prefix = strings.Trim(cfg.Prefix+"/imvault-test-"+rand.Text(), "/")
	s, err := NewS3(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	const key = "media/range-test"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.Delete(ctx, key); err != nil {
			t.Errorf("delete test object: %v", err)
		}
	})
	if _, err := s.Stat(t.Context(), key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing object must be distinguishable: %v", err)
	}
	data := bytes.Repeat([]byte("imvault-check-"), 200000)
	if _, err := s.Save(t.Context(), key, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	r, err := s.Open(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Seek(-37, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	tail, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(tail, data[len(data)-37:]) {
		t.Fatal("provider range mismatch", err)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(all, data) {
		t.Fatal("provider stream mismatch", err)
	}
	if err := s.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(t.Context(), key); !errors.Is(err, ErrNotFound) {
		t.Fatal("provider deletion did not remove object", err)
	}
}
