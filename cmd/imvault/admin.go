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

	"imvault/internal/accounts"
	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/models"
	"imvault/internal/store"
)

func runCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return run()
	}
	switch args[0] {
	case "create-admin":
		return createAdmin(args[1:], stdin, stdout)
	case "refresh-metadata":
		return refreshMetadata(args[1:], stdout)
	case "help", "-h", "--help":
		_, err := fmt.Fprintln(stdout, "Usage: imvault [COMMAND]\n\nWithout arguments, starts the server. Configuration uses IMVAULT_* environment variables.\n\ncreate-admin --username NAME --password-stdin [--email ADDRESS]\n  Creates a new administrator locally; existing accounts are never changed.\nrefresh-metadata\n  Refreshes photo details from stored originals without changing files or sharing settings.")
		return err
	default:
		return fmt.Errorf("unknown command %q; use imvault --help", args[0])
	}
}

func createAdmin(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	flags.SetOutput(stdout)
	username := flags.String("username", "", "administrator username (required)")
	email := flags.String("email", "", "optional email address")
	passwordStdin := flags.Bool("password-stdin", false, "read the password from stdin (required)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || !*passwordStdin || strings.TrimSpace(*username) == "" {
		return errors.New("usage: imvault create-admin --username NAME --password-stdin [--email ADDRESS]")
	}
	password, err := readAdminPassword(stdin)
	if err != nil {
		return err
	}
	name, address := strings.TrimSpace(*username), strings.TrimSpace(*email)
	if err := accounts.ValidateRegistration(name, address, password); err != nil {
		return err
	}
	user, err := provisionAdmin(context.Background(), name, address, password)
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

func provisionAdmin(ctx context.Context, username, email, password string) (*models.User, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}
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
