// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/comfylib/setup"
	"golang.org/x/crypto/bcrypt"

	"imvault/internal/db"
	"imvault/internal/store"
	"imvault/internal/testutil"
)

func TestCommonOwnerCommandsAndSessionRevocation(t *testing.T) {
	path := adminTestEnvironment(t)
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCommand([]string{"setup"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Setup key: ") {
		t.Fatal("setup did not reveal a host key")
	}
	if err := runCommand([]string{"create-owner", "--username", "alex", "--password-stdin"}, strings.NewReader("a private initial password"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := runCommand([]string{"setup"}, strings.NewReader(""), io.Discard); !errors.Is(err, setup.ErrComplete) {
		t.Fatalf("owner did not close browser setup: %v", err)
	}
	database, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, database)
	st := store.New(database)
	user, err := st.UserByUsername(t.Context(), "alex")
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateSession(t.Context(), "session-before-reset", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	secret := "a different private password"
	output.Reset()
	if err = runCommand([]string{"set-password", "--username", "alex", "--password-stdin"}, strings.NewReader(secret+"\n"), &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("password printed")
	}
	updated, err := st.UserByUsername(t.Context(), "alex")
	if err != nil {
		t.Fatal(err)
	}
	if err = bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte(secret)); err != nil {
		t.Fatal("new password was not stored", err)
	}
	if _, err = st.UserBySession(t.Context(), "session-before-reset", time.Now()); err == nil {
		t.Fatal("old session survived password reset")
	}
}

func TestCommonAccountHelpAndRejectedInputDoNotCreateData(t *testing.T) {
	for _, args := range [][]string{
		{"setup", "--help"}, {"create-owner", "--help"}, {"set-password", "--help"},
		{"set-password", "--username", "alex", "--password-stdin"},
		{"create-owner", "--username", "alex", "--password-stdin", "--password-prompt"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			path := adminTestEnvironment(t)
			err := runCommand(args, strings.NewReader("a private test password"), io.Discard)
			if args[len(args)-1] == "--help" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("invalid account command accepted")
			}
			if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("help or invalid input created a database", err)
			}
		})
	}
}

func TestAccountDataDirectoryOverrideAndServiceReexecution(t *testing.T) {
	defaultDB := adminTestEnvironment(t)
	t.Setenv("IMVAULT_DB", "")
	selected := filepath.Join(t.TempDir(), "selected")
	args := []string{"--username", "alex", "--data-dir", selected, "--password-stdin"}
	if err := runCommand(append([]string{"create-owner"}, args...), strings.NewReader("a private initial password"), io.Discard); err != nil {
		t.Fatal(err)
	}
	secret := "a different private password"
	if err := runCommand(append([]string{"set-password"}, args...), strings.NewReader(secret), io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(defaultDB); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("override touched default database", err)
	}
	settings, err := accountChildSettings(servicePaths(), "set-password", args, filepath.Dir(defaultDB))
	if err != nil || settings["IMVAULT_DB"] != filepath.Join(selected, "imvault.db") {
		t.Fatal("service child would open the wrong database", settings, err)
	}
	database, err := db.Open(t.Context(), settings["IMVAULT_DB"])
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, database)
	user, err := store.New(database).UserByUsername(t.Context(), "alex")
	if err != nil {
		t.Fatal(err)
	}
	if err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(secret)); err != nil {
		t.Fatal(err)
	}
	// A value that resembles another option remains a username, as Go's flag
	// parser would read it; it must not alter service database selection.
	settings, err = accountChildSettings(servicePaths(), "set-password", []string{"--username", "--data-dir=elsewhere", "--password-stdin"}, filepath.Dir(defaultDB))
	if err != nil || settings["IMVAULT_DB"] != filepath.Join(filepath.Dir(defaultDB), "imvault.db") {
		t.Fatal("option-like username changed database selection", settings, err)
	}
}
