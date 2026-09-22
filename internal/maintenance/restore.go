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

	"imvault/internal/storage"
	"imvault/internal/store"
)

// Restore produces a complete new local data directory. It never overwrites an
// instance, alters a backup, or requires access to its original S3 credentials.
func Restore(ctx context.Context, input, output string, out io.Writer) error {
	manifest, err := readManifest(input)
	if err != nil {
		return err
	}
	stage, err := stageDirectory(output)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
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
	for i, entry := range entries {
		if _, _, err := CopyVerified(ctx, source, dest, entry); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "[%d/%d] restored %s\n", i+1, len(entries), entry.Key); err != nil {
			return err
		}
	}
	return nil
}

func readManifest(input string) (Manifest, error) {
	var manifest Manifest
	root, err := os.OpenRoot(input)
	if err != nil {
		return manifest, err
	}
	defer root.Close()
	f, err := root.Open("manifest.json")
	if err != nil {
		return manifest, err
	}
	defer f.Close()
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

func validateInventory(ctx context.Context, st *store.Store, objects []Object) error {
	want, err := Inventory(ctx, st)
	if err != nil {
		return err
	}
	if len(want) != len(objects) {
		return errors.New("backup manifest does not match database objects")
	}
	byKey := map[string]Object{}
	for _, obj := range objects {
		byKey[obj.Key] = obj
	}
	for _, expected := range want {
		got, ok := byKey[expected.Key]
		if !ok {
			return fmt.Errorf("backup omits %s", expected.Key)
		}
		if expected.SHA256 != "" && (got.SHA256 != expected.SHA256 || got.Size != expected.Size) {
			return fmt.Errorf("backup original does not match database: %s", expected.Key)
		}
	}
	return nil
}
