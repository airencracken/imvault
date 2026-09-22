// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/instance"
	"imvault/internal/maintenance"
	"imvault/internal/media"
	"imvault/internal/storage"
	"imvault/internal/store"
)

const commandHelp = `Usage: imvault [COMMAND]

Without arguments, starts the server. Configuration uses IMVAULT_* environment variables.

create-admin --username NAME --password-stdin [--email ADDRESS]
  Creates a new administrator locally; existing accounts are never changed.
refresh-metadata
  Refreshes photo details from stored originals without changing sharing settings.
migrate-storage
  Copies and verifies objects to IMVAULT_DEST_* storage. Stop the server first.
rebuild-thumbnails [--videos-only]
  Regenerates thumbnails, previews and video posters. Stop the server first.
backup --output DIRECTORY
  Creates a verified, self-contained backup. Stop the server first.
restore --input DIRECTORY --output DIRECTORY
  Verifies a backup and restores to a new local data directory.`

func runMaintenance(command string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	var output string
	var videosOnly bool
	if command == "backup" {
		flags.StringVar(&output, "output", "", "new backup directory (required)")
	}
	if command == "rebuild-thumbnails" {
		flags.BoolVar(&videosOnly, "videos-only", false, "rebuild only video posters")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || (command == "backup" && output == "") {
		return fmt.Errorf("invalid arguments; use imvault %s --help", command)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return withMaintenance(ctx, cfg, func(st *store.Store, objects storage.Backend) error {
		return performMaintenance(ctx, command, cfg, st, objects, output, videosOnly, out)
	})
}

func withMaintenance(ctx context.Context, cfg *config.Config, run func(*store.Store, storage.Backend) error) error {
	if _, err := os.Stat(cfg.DBPath); err != nil {
		return fmt.Errorf("open existing database: %w", err)
	}
	lock, err := instance.Acquire(cfg.DBPath, true)
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
	return run(store.New(database), objects)
}

func performMaintenance(ctx context.Context, command string, cfg *config.Config, st *store.Store, objects storage.Backend, output string, videosOnly bool, out io.Writer) error {
	switch command {
	case "backup":
		if err := maintenance.Backup(ctx, cfg, st, objects, output, out); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "Backup verified: %s\n", output)
		return err
	case "migrate-storage":
		destConfig, err := config.LoadStorage("IMVAULT_DEST_", "")
		if err != nil {
			return fmt.Errorf("destination: %w", err)
		}
		dest, err := storage.New(ctx, destConfig)
		if err != nil {
			return err
		}
		return maintenance.Migrate(ctx, st, objects, dest, out)
	case "rebuild-thumbnails":
		video := media.NewFFmpeg(cfg.FFmpegPath, cfg.FFprobePath)
		// Previously accepted clips may exceed today's upload duration limit.
		processor := media.NewProcessor(cfg.ThumbMax, cfg.PreviewMax, cfg.JPEGQuality, 0, video)
		return maintenance.Regenerate(ctx, st, objects, processor, videosOnly, out)
	default:
		return errors.New("unknown maintenance command")
	}
}

func restoreBackup(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	flags.SetOutput(out)
	input := flags.String("input", "", "backup directory (required)")
	output := flags.String("output", "", "new local data directory (required)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *input == "" || *output == "" {
		return errors.New("usage: imvault restore --input BACKUP --output NEW_DIRECTORY")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := maintenance.Restore(ctx, *input, *output, out); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "Restore verified: %s\nUse IMVAULT_STORAGE=disk with IMVAULT_DATA_DIR set to this directory. Restore your service configuration separately.\n", *output)
	return err
}
