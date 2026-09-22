// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"imvault/internal/db"
	"imvault/internal/models"
	"imvault/internal/store"
)

func adminTestEnvironment(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.db")
	t.Setenv("IMVAULT_DATA_DIR", dir)
	t.Setenv("IMVAULT_DB", path)
	t.Setenv("IMVAULT_ALLOW_SIGNUP", "false")
	t.Setenv("IMVAULT_INVITE_ONLY", "true")
	t.Setenv("IMVAULT_DEFAULT_QUOTA_BYTES", "12345")
	return path
}

func TestCreateAdminAndRefuseExistingAccount(t *testing.T) {
	path := adminTestEnvironment(t)
	var output bytes.Buffer
	password := " spaces are preserved "
	args := []string{"create-admin", "--username", "marcus", "--email", "marcus@example.com", "--password-stdin"}
	if err := runCommand(args, strings.NewReader(password+"\r\n"), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Created administrator") || strings.Contains(output.String(), password) {
		t.Fatalf("unexpected command output: %q", output.String())
	}
	database, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	st := store.New(database)
	user, err := st.UserByUsername(t.Context(), "marcus")
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != models.RoleAdmin || user.Email != "marcus@example.com" || user.QuotaBytes != 12345 {
		t.Fatalf("unexpected provisioned account: role=%s email=%s quota=%d", user.Role, user.Email, user.QuotaBytes)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		t.Fatalf("password was not stored correctly: %v", err)
	}
	member, err := st.CreateUser(t.Context(), store.NewUser{Username: "existing", PasswordHash: "unchanged", Role: models.RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"MARCUS", "EXISTING"} {
		output.Reset()
		err := runCommand([]string{"create-admin", "--username", name, "--password-stdin"}, strings.NewReader("different-password"), &output)
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("duplicate %s: %v", name, err)
		}
		if output.Len() != 0 {
			t.Errorf("failed command reported success: %q", output.String())
		}
	}
	after, err := st.UserByID(t.Context(), user.ID)
	if err != nil || after.PasswordHash != user.PasswordHash || after.Role != models.RoleAdmin {
		t.Fatal("duplicate command changed the existing administrator")
	}
	after, err = st.UserByID(t.Context(), member.ID)
	if err != nil || after.PasswordHash != member.PasswordHash || after.Role != models.RoleMember {
		t.Fatal("duplicate command promoted or changed the existing member")
	}
}

func TestCreateAdminRejectsInvalidInputBeforeCreatingDatabase(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		password string
	}{
		{"unknown command", []string{"typo"}, "valid-password"},
		{"no password source", []string{"create-admin", "--username", "marcus"}, "valid-password"},
		{"no username", []string{"create-admin", "--password-stdin"}, "valid-password"},
		{"invalid username", []string{"create-admin", "--username", "bad user", "--password-stdin"}, "valid-password"},
		{"invalid email", []string{"create-admin", "--username", "marcus", "--password-stdin", "--email", "not-email"}, "valid-password"},
		{"extra argument", []string{"create-admin", "--username", "marcus", "--password-stdin", "extra"}, "valid-password"},
		{"empty password", []string{"create-admin", "--username", "marcus", "--password-stdin"}, ""},
		{"short password", []string{"create-admin", "--username", "marcus", "--password-stdin"}, "short"},
		{"too many bytes", []string{"create-admin", "--username", "marcus", "--password-stdin"}, strings.Repeat("x", 73)},
		{"multiline password", []string{"create-admin", "--username", "marcus", "--password-stdin"}, "password\nsecond"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := adminTestEnvironment(t)
			if err := runCommand(tc.args, strings.NewReader(tc.password), io.Discard); err == nil {
				t.Fatal("invalid input accepted")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid command touched database: %v", err)
			}
		})
	}
}

func TestReadAdminPasswordBoundaries(t *testing.T) {
	for _, password := range []string{"12345678", strings.Repeat("x", 72), strings.Repeat("é", 36), "  leading and trailing  "} {
		for _, ending := range []string{"", "\n", "\r\n"} {
			got, err := readAdminPassword(strings.NewReader(password + ending))
			if err != nil || got != password {
				t.Errorf("password of %d bytes with %q ending: changed=%t err=%v", len(password), ending, got != password, err)
			}
		}
	}
	reader := strings.NewReader(strings.Repeat("x", 1000000))
	if _, err := readAdminPassword(reader); err == nil || reader.Len() < 999900 {
		t.Fatal("oversized password was accepted or read without a bound")
	}
	if _, err := readAdminPassword(brokenPasswordReader{}); err == nil {
		t.Fatal("input read error was ignored")
	}
}

type brokenPasswordReader struct{}

func (brokenPasswordReader) Read([]byte) (int, error) { return 0, errors.New("input failed") }

func TestConcurrentAdminProvisioningDoesNotOverwrite(t *testing.T) {
	path := adminTestEnvironment(t)
	database, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		workers.Go(func() {
			results <- runCommand([]string{"create-admin", "--username", "marcus", "--password-stdin"}, strings.NewReader("valid-password"), io.Discard)
		})
	}
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !strings.Contains(err.Error(), "already exists") {
			t.Errorf("unexpected concurrent error: %v", err)
		}
	}
	count, err := store.New(database).CountUsers(t.Context())
	if err != nil || successes != 1 || count != 1 {
		t.Fatalf("successes=%d accounts=%d err=%v", successes, count, err)
	}
}
