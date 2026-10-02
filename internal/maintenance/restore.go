// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"imvault/internal/closer"
	"imvault/internal/storage"
	"imvault/internal/store"
)

// Restore produces a complete new local data directory. It never overwrites an
// instance, alters a backup, or requires access to its original S3 credentials.
func Restore(ctx context.Context, input, output string, out io.Writer) (err error) {
	manifest, err := readManifest(input)
	if err != nil {
		return err
	}
	stage, err := stageDirectory(output)
	if err != nil {
		return err
	}
	// Once published, the stage has been renamed away and this is a no-op.
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	source, err := storage.OpenDisk(input)
	if err != nil {
		return err
	}
	dest, err := storage.NewDisk(stage)
	if err != nil {
		return err
	}
	for _, entry := range []Object{manifest.Database, manifest.Secret} {
		if _, _, err := CopyVerified(ctx, source, dest, entry); err != nil {
			return err
		}
		if err := os.Chmod(filepath.Join(stage, entry.Key), 0o600); err != nil {
			return err
		}
	}
	key, err := os.ReadFile(filepath.Join(stage, "secret.key"))
	if err != nil {
		return err
	}
	if err := validateDatabase(ctx, filepath.Join(stage, "imvault.db"), key, manifest.Objects); err != nil {
		return err
	}
	if err := restoreObjects(ctx, input, stage, manifest.Objects, out); err != nil {
		return err
	}
	return publishDirectory(stage, output)
}

func restoreObjects(ctx context.Context, input, stage string, entries []Object, out io.Writer) error {
	objectDir := filepath.Join(input, "objects")
	info, err := os.Lstat(objectDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("backup objects must be a directory, not a symlink")
	}
	source, err := storage.OpenDisk(objectDir)
	if err != nil {
		return err
	}
	dest, err := storage.NewDisk(filepath.Join(stage, "objects"))
	if err != nil {
		return err
	}
	_, err = copyInventory(ctx, source, dest, entries, out, "restored")
	return err
}

func readManifest(input string) (Manifest, error) {
	var manifest Manifest
	root, err := os.OpenRoot(input)
	if err != nil {
		return manifest, err
	}
	defer closer.Discard(root)
	f, err := root.Open("manifest.json")
	if err != nil {
		return manifest, err
	}
	defer closer.Discard(f)
	info, err := f.Stat()
	if err != nil {
		return manifest, err
	}
	if info.Size() > 64<<20 {
		return manifest, errors.New("backup manifest exceeds 64 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 64<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return manifest, errors.New("trailing backup manifest data")
	}
	return manifest, validateManifest(manifest)
}

func validateManifest(m Manifest) error {
	if m.Version != 1 || m.Database.Key != "imvault.db" || m.Secret.Key != "secret.key" || m.Objects == nil {
		return errors.New("unsupported or incomplete backup manifest")
	}
	seen := map[string]bool{}
	for _, obj := range append([]Object{m.Database, m.Secret}, m.Objects...) {
		if err := storage.ValidateKey(obj.Key); err != nil {
			return err
		}
		hash, err := hex.DecodeString(obj.SHA256)
		if err != nil || len(hash) != 32 || obj.Size < 0 {
			return errors.New("invalid backup checksum or size")
		}
		if seen[obj.Key] {
			return errors.New("duplicate backup object key")
		}
		seen[obj.Key] = true
	}
	return nil
}

func openSnapshot(filename string) (*sql.DB, error) {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	u := &url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&immutable=1"}
	return sql.Open("sqlite", u.String())
}

// validateInventory checks a backup's manifest against its own database. Every
// original the database refers to must be present with its recorded hash, and
// the manifest may name nothing else. A derived object may be absent: the
// backup skipped it because it was already missing.
func validateInventory(ctx context.Context, st *store.Store, objects []Object) error {
	want, err := Inventory(ctx, st)
	if err != nil {
		return err
	}
	expected := make(map[string]Object, len(want))
	for _, entry := range want {
		expected[entry.Key] = entry
	}
	present := make(map[string]Object, len(objects))
	for _, obj := range objects {
		if _, ok := expected[obj.Key]; !ok {
			return fmt.Errorf("backup manifest names %s, which the database does not", obj.Key)
		}
		present[obj.Key] = obj
	}
	for _, entry := range want {
		got, ok := present[entry.Key]
		if !ok {
			if derived(entry) {
				continue
			}
			return fmt.Errorf("backup omits %s", entry.Key)
		}
		if !derived(entry) && (got.SHA256 != entry.SHA256 || got.Size != entry.Size) {
			return fmt.Errorf("backup original does not match database: %s", entry.Key)
		}
	}
	return nil
}
