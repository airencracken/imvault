// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/airencracken/comfylib/autobackup"
	"imvault/internal/config"
	"imvault/internal/maintenance"
	"imvault/internal/storage"
	"imvault/internal/store"
)

func automaticBackups(cfg *config.Config, st *store.Store, objects storage.Backend, logger *slog.Logger) (*autobackup.Runner, error) {
	c, err := autobackup.Load("imvault", cfg.DataDir, os.Getenv)
	if err != nil {
		return nil, err
	}
	return autobackup.New(c, func(ctx context.Context, dest string, limit int64) error {
		return maintenance.BackupLimited(ctx, cfg, st, objects, dest, limit, io.Discard)
	}, logger)
}
