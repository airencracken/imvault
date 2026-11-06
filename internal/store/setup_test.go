// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"imvault/internal/db"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/airencracken/comfylib/setup"
)

func TestBrowserFirstOwnerContract(t *testing.T) {
	ctx := context.Background()
	data := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(ctx, filepath.Join(data, "imvault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := New(database)
	ready, err := s.HasOwner(ctx)
	if err != nil || ready {
		t.Fatal("fresh instance reports an owner", ready, err)
	}
	for _, invalid := range []setup.Owner{
		{Username: "bad user", Password: "a private first owner password"},
		{Username: "alex", Password: "short"},
	} {
		if err = s.CreateFirstOwner(ctx, invalid, 5<<30); err == nil {
			t.Fatal("browser callback accepted invalid credentials")
		}
	}
	ready, err = s.HasOwner(ctx)
	if err != nil || ready {
		t.Fatal("invalid credentials initialized the instance", ready, err)
	}
	owner := setup.Owner{Username: "alex", Password: "a private first owner password"}
	if err = s.CreateFirstOwner(ctx, owner, 5<<30); err != nil {
		t.Fatal(err)
	}
	ready, err = s.HasOwner(ctx)
	if err != nil || !ready {
		t.Fatal("first account is not an owner", err)
	}
	owner.Username = "second"
	err = s.CreateFirstOwner(ctx, owner, 5<<30)
	if !errors.Is(err, setup.ErrComplete) {
		t.Fatal("second browser owner created", err)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil || count != 1 {
		t.Fatal("setup changed existing users", count, err)
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE users SET disabled=1 WHERE username='alex'"); err != nil {
		t.Fatal(err)
	}
	ready, err = s.HasOwner(ctx)
	if err != nil || !ready {
		t.Fatal("suspension reopened browser setup", err)
	}
}
func TestBrowserFirstOwnerConcurrentCreation(t *testing.T) {
	ctx := context.Background()
	data := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(ctx, filepath.Join(data, "imvault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := New(database)
	s.db.SetMaxOpenConns(4)
	var wg sync.WaitGroup
	results := make(chan error, 6)
	owner := setup.Owner{Username: "alex", Password: "a private first owner password"}
	for i := range 6 {
		wg.Go(func() {
			owner := owner
			owner.Username = fmt.Sprintf("owner_%d", i)
			results <- s.CreateFirstOwner(ctx, owner, 5<<30)
		})
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatal("browser setup had multiple winners", wins)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent setup was not atomic", count, err)
	}
}
