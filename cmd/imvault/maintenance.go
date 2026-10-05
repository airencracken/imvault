// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"database/sql"
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

func runMaintenance(command string, args []string, out io.Writer) error {
	flags := commandFlags(command, out)
	var output, outputDir string
	var keep int
	var videosOnly bool
	if command == "backup" {
		flags.StringVar(&output, "output", "", "new backup directory")
		flags.StringVar(&outputDir, "output-dir", "", "parent directory for a dated backup (alternative to --output)")
		flags.IntVar(&keep, "keep", 7, "completed scheduled backups to retain with --output-dir (0 keeps all)")
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
	if flags.NArg() != 0 || (command == "backup" && (output == "") == (outputDir == "")) {
		return fmt.Errorf("invalid arguments; use imvault %s --help", command)
	}
	if err := validateRetention(flags, outputDir, keep); err != nil {
		return err
	}
	if err := refuseRootMaintenance(command); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return withMaintenanceMode(ctx, cfg, maintenanceOpener(command), command != "backup", func(st *store.Store, objects storage.Backend) error {
		if command == "backup" && outputDir != "" {
			_, err := maintenance.ScheduledBackup(ctx, cfg, st, objects, outputDir, keep, out)
			return err
		}
		return performMaintenance(ctx, command, cfg, st, objects, output, videosOnly, out)
	})
}

func validateRetention(flags *flag.FlagSet, outputDir string, keep int) error {
	explicit := false
	flags.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "keep" })
	if keep < 0 || (explicit && outputDir == "") {
		return errors.New("--keep requires --output-dir and a nonnegative count")
	}
	return nil
}

// maintenanceOpener picks how a command opens the database. Commands that
// change the collection bring the schema up to date first, as the server does.
// A backup must copy the database exactly as it stands, so it neither migrates
// nor reads a schema other than this binary's own; see db.OpenCurrent. Every
// opener refuses a database a newer Imvault has migrated.
func maintenanceOpener(command string) func(context.Context, string) (*sql.DB, error) {
	if command == "backup" {
		return db.OpenCurrent
	}
	return db.Open
}

func withMaintenance(ctx context.Context, cfg *config.Config, open func(context.Context, string) (*sql.DB, error), run func(*store.Store, storage.Backend) error) (err error) {
	return withMaintenanceMode(ctx, cfg, open, true, run)
}

func withMaintenanceMode(ctx context.Context, cfg *config.Config, open func(context.Context, string) (*sql.DB, error), exclusive bool, run func(*store.Store, storage.Backend) error) (err error) {
	if _, err := os.Stat(cfg.DBPath); err != nil {
		return fmt.Errorf("open existing database: %w", err)
	}
	lock, err := instance.Acquire(cfg.DBPath, exclusive)
	if err != nil {
		return err
	}
	defer closeInto(&err, lock)
	database, err := open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer closeInto(&err, database)
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
		video, err := media.ConfiguredVideo(ctx, cfg.FFmpegPath, cfg.FFprobePath, cfg.MediaSandbox, cfg.BwrapPath)
		if err != nil {
			return err
		}
		// Previously accepted clips may exceed today's upload duration limit.
		processor := media.NewProcessor(cfg.ThumbMax, cfg.PreviewMax, cfg.JPEGQuality, 0, video)
		return maintenance.Regenerate(ctx, st, objects, processor, videosOnly, out)
	default:
		return errors.New("unknown maintenance command")
	}
}

func restoreBackup(args []string, out io.Writer) error {
	flags := commandFlags("restore", out)
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
