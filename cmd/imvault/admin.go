// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/airencracken/comfylib/proxyconfig"
	"github.com/airencracken/comfylib/svcconfig"
	"golang.org/x/crypto/bcrypt"

	"imvault/contrib"
	"imvault/internal/accounts"
	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/instance"
	"imvault/internal/models"
	"imvault/internal/store"
)

func runCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return run()
	}
	if args[0] == "sandbox" {
		return runSandbox(args[1:], stdout)
	}
	switch args[0] {
	case "version":
		return printVersion(args[1:], stdout)
	case "--version":
		if len(args) != 1 {
			return errors.New("usage: imvault --version")
		}
		return printVersion(nil, stdout)
	case "serve":
		return serve(args[1:], stdout)
	case "proxy-config":
		return proxyconfig.Run(contrib.ProxySpec, args[1:], stdout)
	case "setup", "set-password", "create-owner", "create-admin":
		return accountCommand(args[0], args[1:], stdin, stdout)
	case "refresh-metadata":
		return refreshMetadata(args[1:], stdout)
	case "migrate-storage", "rebuild-thumbnails", "backup":
		return runMaintenance(args[0], args[1:], stdout)
	case "restore":
		return restoreBackup(args[1:], stdout)
	case "help":
		return commandHelpFor(args, stdin, stdout)
	case "-h", "--help":
		if len(args) != 1 {
			return errors.New("use imvault help COMMAND for command-specific help")
		}
		_, err := fmt.Fprintln(stdout, commandHelp)
		return err
	default:
		return fmt.Errorf("unknown command %q; use imvault --help", args[0])
	}
}

func accountCommand(command string, args []string, in io.Reader, out io.Writer) error {
	switch command {
	case "setup":
		return setupCommand(args, out, servicePaths())
	case "set-password":
		return setAccountPassword(args, in, out, servicePaths())
	default:
		return ownerCommand(command, args, in, out, servicePaths())
	}
}

func createAdmin(args []string, stdin io.Reader, stdout io.Writer) error {
	return createAdminWithConfigPaths(args, stdin, stdout, servicePaths())
}

func createAdminWithConfigPaths(args []string, stdin io.Reader, stdout io.Writer, paths svcconfig.Paths) error {
	return ownerCommand("create-admin", args, stdin, stdout, paths)
}
func ownerCommand(command string, args []string, stdin io.Reader, stdout io.Writer, paths svcconfig.Paths) error {
	options := accountFlags(command, stdout)
	flags, username, email, source := options.flags, options.username, options.email, options.source
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*username) == "" {
		return fmt.Errorf("usage: imvault %s --username NAME [--password-prompt | --password-stdin | --password-file PATH] [--data-dir DIRECTORY] [--email ADDRESS]", command)
	}
	secret, err := source.Read(stdin, stdout)
	if err != nil {
		return err
	}
	name, address := strings.TrimSpace(*username), strings.TrimSpace(*email)
	if err := accounts.ValidateRegistration(name, address, secret); err != nil {
		return err
	}
	dataDir, err := accountDataDirectory(paths, *options.data)
	if err != nil {
		return err
	}
	settings, err := paths.Settings("IMVAULT_DB")
	if err != nil {
		return err
	}
	user, err := provisionAdmin(context.Background(), name, address, secret, dataDir, settings["IMVAULT_DB"])
	if err != nil {
		return err
	}
	if command == "create-owner" {
		_, err = fmt.Fprintf(stdout, "Owner account created: %s.\n", user.Username)
	} else {
		_, err = fmt.Fprintf(stdout, "Created administrator %q (ID %d).\n", user.Username, user.ID)
	}
	return err
}

// readAdminPassword accepts one line, with an optional LF or CRLF terminator.
// Spaces remain part of the password, and oversized input is never buffered.
func readAdminPassword(stdin io.Reader) (string, error) {
	input, err := io.ReadAll(io.LimitReader(stdin, accounts.MaxPasswordBytes+3))
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	password := string(input)
	if strings.HasSuffix(password, "\n") {
		password = strings.TrimSuffix(strings.TrimSuffix(password, "\n"), "\r")
	}
	if strings.ContainsAny(password, "\r\n") {
		return "", errors.New("password stdin must contain a single line")
	}
	if err := accounts.ValidatePassword(password); err != nil {
		return "", err
	}
	return password, nil
}

func provisionAdmin(ctx context.Context, username, email, password, dataDir, dbPath string) (_ *models.User, err error) {
	cfg, err := config.LoadWithDataDirectory(dataDir, dbPath)
	if err != nil {
		return nil, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}
	lock, err := instance.Acquire(cfg.DBPath, false)
	if err != nil {
		return nil, err
	}
	defer closeInto(&err, lock)
	database, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer closeInto(&err, database)

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	user, err := store.New(database).CreateUser(ctx, store.NewUser{
		Username:     username,
		Email:        email,
		PasswordHash: string(hash),
		Role:         models.RoleAdmin,
		QuotaBytes:   cfg.DefaultQuotaBytes,
	})
	if errors.Is(err, store.ErrConflict) {
		return nil, fmt.Errorf("account %q already exists; no account was changed", username)
	}
	return user, err
}

func commandHelpFor(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 2 && (commandDescriptions[args[1]] != "" || args[1] == "proxy-config") {
		return runCommand([]string{args[1], "--help"}, stdin, stdout)
	}
	if len(args) != 1 {
		return errors.New("usage: imvault help [COMMAND]; use imvault --help for commands")
	}
	_, err := fmt.Fprintln(stdout, commandHelp)
	return err
}
