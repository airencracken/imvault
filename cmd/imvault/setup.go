// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/instance"
	"imvault/internal/storage"
	"imvault/internal/store"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/airencracken/comfylib/autobackup"
	"github.com/airencracken/comfylib/password"
	"github.com/airencracken/comfylib/setup"
	"github.com/airencracken/comfylib/svcconfig"
	"golang.org/x/crypto/bcrypt"
)

func firstSetup(st *store.Store, cfg *config.Config) (*setup.Handler, error) {
	return setup.New(setup.Config{App: "imvault", Directory: cfg.DataDir, BaseURL: cfg.BaseURL, Address: cfg.Addr, Ready: st.HasOwner, Create: func(ctx context.Context, owner setup.Owner) error {
		return st.CreateFirstOwner(ctx, owner, cfg.DefaultQuotaBytes)
	}})
}
func setupCommand(args []string, out io.Writer, paths svcconfig.Paths) (err error) {
	options := accountFlags("setup", out)
	flags, data := options.flags, options.data
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if err := refuseRootMaintenance("setup"); err != nil {
		return err
	}
	if *data == "" {
		*data, err = paths.DataDir("IMVAULT_DATA_DIR")
	} else {
		*data, err = filepath.Abs(*data)
	}
	if err != nil {
		return err
	}
	settings, err := paths.Settings("IMVAULT_DB")
	if err != nil {
		return err
	}
	cfg, err := config.LoadWithDataDirectory(*data, settings["IMVAULT_DB"])
	if err != nil {
		return err
	}
	if err = cfg.EnsureDirs(); err != nil {
		return err
	}
	lock, err := instance.Acquire(cfg.DBPath, false)
	if err != nil {
		return err
	}
	defer closeInto(&err, lock)
	database, err := db.Open(context.Background(), cfg.DBPath)
	if err != nil {
		return err
	}
	defer closeInto(&err, database)
	wizard, err := firstSetup(store.New(database), cfg)
	if err != nil {
		return err
	}
	return wizard.Enable(context.Background(), out)
}
func setAccountPassword(args []string, in io.Reader, out io.Writer, paths svcconfig.Paths) (err error) {
	options := accountFlags("set-password", out)
	flags, username, source := options.flags, options.username, options.source
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *username == "" {
		return errors.New("provide --username")
	}
	secret, err := source.Read(in, out)
	if err != nil {
		return err
	}
	if err = refuseRootMaintenance("set-password"); err != nil {
		return err
	}
	cfg, err := passwordCommandConfig(paths, *options.data)
	if err != nil {
		return err
	}
	if _, err = os.Stat(cfg.DBPath); err != nil {
		return err
	}
	lock, err := instance.Acquire(cfg.DBPath, false)
	if err != nil {
		return err
	}
	defer closeInto(&err, lock)
	database, err := db.Open(context.Background(), cfg.DBPath)
	if err != nil {
		return err
	}
	defer closeInto(&err, database)
	st := store.New(database)
	user, err := st.UserByUsername(context.Background(), *username)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err = st.SetPassword(context.Background(), user.ID, string(hash)); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Password changed; earlier sessions have been revoked.")
	return err
}

func runSetupBackups(ctx context.Context, wizard *setup.Handler, backups *autobackup.Runner) func() {
	backupCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if wizard.Wait(backupCtx) == nil {
			backups.Run(backupCtx)
		}
	}()
	return func() { cancel(); <-done }
}

func passwordCommandConfig(paths svcconfig.Paths, selected string) (*config.Config, error) {
	data, err := accountDataDirectory(paths, selected)
	if err != nil {
		return nil, err
	}
	settings, err := paths.Settings("IMVAULT_DB")
	if err != nil {
		return nil, err
	}
	return config.LoadWithDataDirectory(data, settings["IMVAULT_DB"])
}

func instanceSetup(cfg *config.Config, st *store.Store, objects storage.Backend, logger *slog.Logger) (*setup.Handler, *autobackup.Runner, error) {
	wizard, err := firstSetup(st, cfg)
	if err != nil {
		return nil, nil, err
	}
	backups, err := automaticBackups(cfg, st, objects, logger)
	if err != nil {
		return nil, nil, err
	}
	return wizard, backups, nil
}

// accountOptions keeps interactive commands and service re-execution on the same
// flag parser. No password is read while resolving service settings.
type accountOptions struct {
	flags                 *flag.FlagSet
	data, username, email *string
	source                *password.Source
}

func accountFlags(command string, out io.Writer) *accountOptions {
	o := &accountOptions{flags: commandFlags(command, out)}
	o.data = o.flags.String("data-dir", "", "private data directory (defaults to the installed service)")
	if command != "setup" {
		o.username = o.flags.String("username", "", "account username (required)")
		o.source = password.Flags(o.flags)
	}
	if command == "create-owner" || command == "create-admin" {
		o.email = o.flags.String("email", "", "optional email address")
	}
	return o
}

func accountDataDirectory(paths svcconfig.Paths, selected string) (string, error) {
	if selected != "" {
		return filepath.Abs(selected)
	}
	return paths.DataDir("IMVAULT_DATA_DIR")
}

func accountChildSettings(paths svcconfig.Paths, command string, args []string, data string) (map[string]string, error) {
	switch command {
	case "setup", "create-owner", "create-admin", "set-password":
		o := accountFlags(command, io.Discard)
		if err := o.flags.Parse(args); err != nil {
			return nil, err
		}
		if *o.data != "" {
			selected, err := filepath.Abs(*o.data)
			if err != nil {
				return nil, err
			}
			data = selected
		}
	}
	return childCommandSettings(paths, command, data)
}
