// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/instance"
	"imvault/internal/metadata"
	"imvault/internal/models"
	"imvault/internal/storage"
	"imvault/internal/store"
)

func refreshMetadata(args []string, stdout io.Writer) error {
	flags := commandFlags("refresh-metadata", stdout)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: imvault refresh-metadata")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfg.DBPath); err != nil {
		return fmt.Errorf("open existing database: %w", err)
	}
	ctx := context.Background()
	lock, err := instance.Acquire(cfg.DBPath, false)
	if err != nil {
		return err
	}
	defer lock.Close()
	database, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer database.Close()
	objects, err := storage.New(ctx, cfg.Storage)
	if err != nil {
		return err
	}
	return refreshStoredMetadata(ctx, store.New(database), objects, stdout)
}

func refreshStoredMetadata(ctx context.Context, st *store.Store, objects storage.Backend, stdout io.Writer) error {
	after := ""
	checked, updated := 0, 0
	for {
		blobs, err := st.BlobsAfter(ctx, after, 100)
		if err != nil {
			return err
		}
		if len(blobs) == 0 {
			break
		}
		for _, blob := range blobs {
			changed, err := refreshBlobMetadata(ctx, st, objects, blob)
			if err != nil {
				return fmt.Errorf("refresh %s after updating %d originals: %w", blob.ObjectKey, updated, err)
			}
			checked++
			if changed {
				updated++
			}
			after = blob.SHA256
		}
	}
	_, err := fmt.Fprintf(stdout, "Checked %d originals; updated %d. Originals and sharing settings were preserved.\n", checked, updated)
	return err
}

func refreshBlobMetadata(ctx context.Context, st *store.Store, objects storage.Backend, blob *models.Blob) (bool, error) {
	src, err := objects.Open(ctx, blob.ObjectKey)
	if err != nil {
		return false, err
	}
	defer src.Close()
	details, err := metadata.Extract(src)
	// Unsupported formats and metadata parse failures do not erase previously
	// extracted fields. A later parser improvement can retry the same original.
	if err != nil || details.Empty() {
		return false, nil
	}
	encoded := details.Encode()
	if encoded == "" || encoded == blob.Details {
		return false, nil
	}
	if err := st.SetBlobDetails(ctx, blob.SHA256, encoded); err != nil {
		return false, err
	}
	return true, nil
}
