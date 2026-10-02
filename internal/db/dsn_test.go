// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// A data directory may be named anything. Before the path was escaped, "?"
// ended the file name and "#" was dropped, so the database landed elsewhere.
func TestDatabasePathsWithURICharactersOpenTheRightFile(t *testing.T) {
	for _, name := range []string{"what?", "hash#tag", "per%cent", "two words", "a&b=c"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "imvault.db")
		database, err := Open(t.Context(), path)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%q: the database is not where it was asked to be: %v", name, err)
		}
	}
}

// Two processes sharing the file, as the server and refresh-metadata do, each
// read then write inside a transaction. Deferred transactions would fail one of
// them with SQLITE_BUSY instead of waiting.
func TestReadThenWriteTransactionsFromTwoHandlesWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	first, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, h := range []*sql.DB{first, second} {
			if err := h.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	if _, err := first.Exec(`CREATE TABLE counter (n INTEGER NOT NULL); INSERT INTO counter VALUES (0)`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for _, database := range []*sql.DB{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				tx, err := database.BeginTx(context.Background(), nil)
				if err != nil {
					errs <- err
					return
				}
				var n int
				if err := tx.QueryRow(`SELECT n FROM counter`).Scan(&n); err != nil {
					errs <- errors.Join(err, tx.Rollback())
					return
				}
				if _, err := tx.Exec(`UPDATE counter SET n = ?`, n+1); err != nil {
					errs <- errors.Join(err, tx.Rollback())
					return
				}
				if err := tx.Commit(); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("a read-then-write transaction failed instead of waiting: %v", err)
	}
	var n int
	if err := first.QueryRow(`SELECT n FROM counter`).Scan(&n); err != nil || n != 200 {
		t.Fatalf("counter = %d (%v), want 200: an increment was lost", n, err)
	}
}
