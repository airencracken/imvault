// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"imvault/internal/config"
	"imvault/internal/secrets"
	"imvault/internal/storage"
	"imvault/internal/store"
)

type Manifest struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Database  Object    `json:"database"`
	Secret    Object    `json:"secret"`
	Objects   []Object  `json:"objects"`
}

// Backup requires the caller's exclusive instance lock for the entire copy.
// The output appears only after every database, key and object check succeeds.
func Backup(ctx context.Context, cfg *config.Config, st *store.Store, source storage.Backend, output string, out io.Writer) error {
	stage, err := stageDirectory(output)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if _, err := st.DB().ExecContext(ctx, `VACUUM INTO ?`, filepath.Join(stage, "imvault.db")); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	key, err := backupKey(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "secret.key"), key, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(stage, "imvault.db"), 0o600); err != nil {
		return err
	}
	if err := validateDatabase(ctx, filepath.Join(stage, "imvault.db"), key, nil); err != nil {
		return err
	}
	manifest, err := backupObjects(ctx, st, source, stage, out)
	if err != nil {
		return err
	}
	root, err := storage.NewDisk(stage)
	if err != nil {
		return err
	}
	manifest.Database, err = Fingerprint(ctx, root, "imvault.db")
	if err != nil {
		return err
	}
	manifest.Secret, err = Fingerprint(ctx, root, "secret.key")
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	return publishDirectory(stage, output)
}

func backupObjects(ctx context.Context, st *store.Store, source storage.Backend, stage string, out io.Writer) (Manifest, error) {
	manifest := Manifest{Version: 1, CreatedAt: time.Now().UTC(), Objects: []Object{}}
	entries, err := Inventory(ctx, st)
	if err != nil {
		return manifest, err
	}
	dest, err := storage.NewDisk(filepath.Join(stage, "objects"))
	if err != nil {
		return manifest, err
	}
	for i, entry := range entries {
		actual, _, err := CopyVerified(ctx, source, dest, entry)
		if err != nil {
			return manifest, fmt.Errorf("backup %s: %w", entry.Key, err)
		}
		manifest.Objects = append(manifest.Objects, actual)
		if _, err := fmt.Fprintf(out, "[%d/%d] backed up %s\n", i+1, len(entries), entry.Key); err != nil {
			return manifest, err
		}
	}
	return manifest, nil
}

func backupKey(cfg *config.Config) ([]byte, error) {
	key := []byte(cfg.SecretKey)
	if len(key) == 0 {
		var err error
		key, err = os.ReadFile(cfg.SecretKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read existing secret key: %w", err)
		}
	}
	if _, err := secrets.Load("", string(key)); err != nil {
		return nil, err
	}
	return append(bytes.TrimSpace(key), '\n'), nil
}

func stageDirectory(output string) (string, error) {
	if output == "" {
		return "", errors.New("an output directory is required")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return "", errors.New("output must be a new directory")
	}
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, ".imvault-maintenance-*")
}

func publishDirectory(stage, output string) error {
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return errors.New("output already exists; it was not replaced")
	}
	if err := syncTree(stage); err != nil {
		return err
	}
	if err := os.Rename(stage, output); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(output))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// syncTree flushes files before the directory is published as a complete backup.
func syncTree(root string) error {
	return filepath.WalkDir(root, func(name string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Sync()
	})
}

// validateDatabase opens a copied snapshot read-only: restore verification must
// not migrate it, run write triggers, or modify the source backup.
func validateDatabase(ctx context.Context, filename string, key []byte, objects []Object) error {
	database, err := openSnapshot(filename)
	if err != nil {
		return err
	}
	defer database.Close()
	var integrity string
	if err := database.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("database integrity check: %s", integrity)
	}
	rows, err := database.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	broken := rows.Next()
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return rowErr
	}
	if broken {
		return errors.New("database has broken foreign keys")
	}
	if err := validateSecrets(ctx, database, key); err != nil {
		return err
	}
	if objects == nil {
		return nil
	}
	return validateInventory(ctx, store.New(database), objects)
}

func validateSecrets(ctx context.Context, database *sql.DB, key []byte) error {
	cipher, err := secrets.Load("", string(key))
	if err != nil {
		return err
	}
	rows, err := database.QueryContext(ctx, `SELECT totp_secret FROM users WHERE totp_secret != ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var secret string
		if err := rows.Scan(&secret); err != nil {
			return err
		}
		if _, err := cipher.Decrypt(secret); err != nil {
			return errors.New("secret.key cannot decrypt the database's two-factor secrets")
		}
	}
	return rows.Err()
}
