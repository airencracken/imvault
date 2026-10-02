// SPDX-License-Identifier: AGPL-3.0-or-later

// Package maintenance implements operations run with the instance stopped.
package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"

	"imvault/internal/closer"
	"imvault/internal/models"
	"imvault/internal/storage"
	"imvault/internal/store"
)

type Object struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func eachBlob(ctx context.Context, st *store.Store, visit func(*models.Blob) error) error {
	after := ""
	for {
		batch, err := st.BlobsAfter(ctx, after, 100)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, blob := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(blob); err != nil {
				return fmt.Errorf("%s: %w", blob.ObjectKey, err)
			}
			after = blob.SHA256
		}
	}
}

// Inventory includes every object named by content that something still
// refers to, not unrelated content elsewhere in the bucket. Originals carry
// their recorded hash and must still match it. Derived objects — thumbnails,
// previews and metadata-free copies — carry none: they can be rebuilt, and a
// missing one is skipped rather than allowed to stop the whole operation.
//
// Content nothing refers to is left out. It is waiting for the server's sweep,
// which may already have deleted some of its objects.
func Inventory(ctx context.Context, st *store.Store) ([]Object, error) {
	var missing int
	err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM files f LEFT JOIN blobs b ON b.sha256 = f.sha256 WHERE b.sha256 IS NULL`).Scan(&missing)
	if err != nil {
		return nil, err
	}
	if missing != 0 {
		return nil, fmt.Errorf("%d files have no content record", missing)
	}
	entries := map[string]Object{}
	err = eachBlob(ctx, st, func(blob *models.Blob) error {
		if err := storage.ValidateKey(blob.ObjectKey); err != nil {
			return errors.New("content record has no valid original key")
		}
		for _, key := range blob.Keys() {
			if err := storage.ValidateKey(key); err != nil {
				return err
			}
			item := Object{Key: key, Size: -1}
			if key == blob.ObjectKey {
				item.Size, item.SHA256 = blob.Size, blob.SHA256
			}
			old, ok := entries[key]
			if ok && old.SHA256 != "" && item.SHA256 != "" && old != item {
				return fmt.Errorf("conflicting originals use object key %s", key)
			}
			if !ok || old.SHA256 == "" {
				entries[key] = item
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]Object, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func Fingerprint(ctx context.Context, objects storage.Backend, key string) (Object, error) {
	f, err := objects.Open(ctx, key)
	if err != nil {
		return Object{}, err
	}
	defer closer.Discard(f)
	hash := sha256.New()
	n, err := io.Copy(hash, storage.ContextReader(ctx, f))
	if err != nil {
		return Object{}, err
	}
	return Object{Key: key, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func verify(ctx context.Context, objects storage.Backend, want Object) error {
	got, err := Fingerprint(ctx, objects, want.Key)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("checksum or size mismatch for %s", want.Key)
	}
	return nil
}

// CopyVerified is resumable without a journal: already matching objects are
// reverified and skipped. Differing destination content is never overwritten.
func CopyVerified(ctx context.Context, source, dest storage.Backend, want Object) (Object, bool, error) {
	actual, err := Fingerprint(ctx, source, want.Key)
	if err != nil {
		return Object{}, false, err
	}
	if want.SHA256 != "" && (actual.SHA256 != want.SHA256 || actual.Size != want.Size) {
		return actual, false, fmt.Errorf("source checksum or size mismatch for %s", want.Key)
	}
	if _, err := dest.Stat(ctx, want.Key); err == nil {
		return actual, false, verify(ctx, dest, actual)
	} else if !errors.Is(err, storage.ErrNotFound) {
		return actual, false, err
	}
	src, err := source.Open(ctx, want.Key)
	if err != nil {
		return actual, false, err
	}
	defer closer.Discard(src)
	n, err := dest.Save(ctx, want.Key, storage.ContextReader(ctx, src))
	if err != nil {
		return actual, false, err
	}
	if n != actual.Size {
		return actual, false, io.ErrShortWrite
	}
	return actual, true, verify(ctx, dest, actual)
}

func Migrate(ctx context.Context, st *store.Store, source, dest storage.Backend, out io.Writer) error {
	entries, err := Inventory(ctx, st)
	if err != nil {
		return err
	}
	if _, err := copyInventory(ctx, source, dest, entries, out, "verified"); err != nil {
		return fmt.Errorf("migration stopped (rerun to resume): %w", err)
	}
	_, err = fmt.Fprintln(out, "Migration verified. Source objects were retained. Configure the destination as IMVAULT_STORAGE before restarting.")
	return err
}

// derived reports whether an inventory entry can be rebuilt from its original.
func derived(entry Object) bool { return entry.SHA256 == "" }

// copyInventory copies and verifies every entry, reporting progress with verb.
// A derived object missing from the source is skipped and reported; a missing
// original is an error. It returns the entries that were copied.
func copyInventory(ctx context.Context, source, dest storage.Backend, entries []Object, out io.Writer, verb string) ([]Object, error) {
	copied := make([]Object, 0, len(entries))
	for i, entry := range entries {
		actual, _, err := CopyVerified(ctx, source, dest, entry)
		if err != nil && derived(entry) && errors.Is(err, storage.ErrNotFound) {
			if _, err := fmt.Fprintf(out, "[%d/%d] skipped %s: the derived copy is missing and can be rebuilt\n", i+1, len(entries), entry.Key); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Key, err)
		}
		copied = append(copied, actual)
		if _, err := fmt.Fprintf(out, "[%d/%d] %s %s\n", i+1, len(entries), verb, entry.Key); err != nil {
			return nil, err
		}
	}
	return copied, nil
}
