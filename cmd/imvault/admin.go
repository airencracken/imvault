// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

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
	switch args[0] {
	case "serve":
		return serve(args[1:], stdout)
	case "proxy-config":
		return proxyconfig.Run(args[1:], stdout)
	case "create-admin":
		return createAdmin(args[1:], stdin, stdout)
	case "refresh-metadata":
		return refreshMetadata(args[1:], stdout)
	case "migrate-storage", "rebuild-thumbnails", "backup":
		return runMaintenance(args[0], args[1:], stdout)
	case "restore":
		return restoreBackup(args[1:], stdout)
	case "help":
		if len(args) == 2 && (commandDescriptions[args[1]] != "" || args[1] == "proxy-config") {
			return runCommand([]string{args[1], "--help"}, stdin, stdout)
		}
		if len(args) != 1 {
			return errors.New("usage: imvault help [COMMAND]; use imvault --help for commands")
		}
		fallthrough
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

func createAdmin(args []string, stdin io.Reader, stdout io.Writer) error {
	return createAdminWithConfigPaths(args, stdin, stdout, defaultProvisioningConfigPaths())
}

func createAdminWithConfigPaths(args []string, stdin io.Reader, stdout io.Writer, paths provisioningConfigPaths) error {
	flags := commandFlags("create-admin", stdout)
	username := flags.String("username", "", "administrator username (required)")
	email := flags.String("email", "", "optional email address")
	passwordPrompt := flags.Bool("password-prompt", false, "prompt twice without echoing (requires a terminal)")
	passwordStdin := flags.Bool("password-stdin", false, "read the password from standard input")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*username) == "" || *passwordPrompt == *passwordStdin {
		return errors.New("usage: imvault create-admin --username NAME (--password-prompt | --password-stdin) [--email ADDRESS]")
	}
	var password string
	var err error
	if *passwordPrompt {
		password, err = readPromptAdminPassword()
	} else {
		password, err = readAdminPassword(stdin)
	}
	if err != nil {
		return err
	}
	name, address := strings.TrimSpace(*username), strings.TrimSpace(*email)
	if err := accounts.ValidateRegistration(name, address, password); err != nil {
		return err
	}
	dataDir, err := resolveProvisioningDataDir(paths)
	if err != nil {
		return err
	}
	dbPath, _, err := resolveProvisioningDBPath(paths)
	if err != nil {
		return err
	}
	user, err := provisionAdmin(context.Background(), name, address, password, dataDir, dbPath)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Created administrator %q (ID %d).\n", user.Username, user.ID)
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

func provisionAdmin(ctx context.Context, username, email, password, dataDir, dbPath string) (*models.User, error) {
	cfg, err := config.LoadWithProvisioningPaths(dataDir, dbPath)
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
	defer lock.Close()
	database, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer database.Close()

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
