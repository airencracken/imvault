// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Processes starting together must agree on one key. Before creation was
// exclusive, each could write its own and the last writer won, leaving the
// others with secrets nobody could read again.
func TestConcurrentFirstStartsAgreeOnOneKey(t *testing.T) {
	for round := 0; round < 20; round++ {
		keyFile := filepath.Join(t.TempDir(), "nested", "secret.key")
		const starters = 8
		ciphers := make([]*Cipher, starters)
		errs := make([]error, starters)
		var wg sync.WaitGroup
		for i := range starters {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ciphers[i], errs[i] = Load(keyFile, "")
			}()
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d, starter %d: %v", round, i, err)
			}
		}
		sealed, err := ciphers[0].Encrypt("shared secret")
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range ciphers[1:] {
			if plain, err := c.Decrypt(sealed); err != nil || plain != "shared secret" {
				t.Fatalf("round %d: starter %d holds a different key", round, i+1)
			}
		}
		entries, err := os.ReadDir(filepath.Dir(keyFile))
		if err != nil || len(entries) != 1 {
			t.Fatalf("round %d: temporary files were left behind: %v %v", round, entries, err)
		}
	}
}

func TestAnUnreadableKeyFileIsNeverReplaced(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(keyFile, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(keyFile, ""); err == nil {
		t.Fatal("a corrupt key file was accepted")
	}
	data, err := os.ReadFile(keyFile)
	if err != nil || string(data) != "not a key\n" {
		t.Fatalf("the key file was rewritten: %q %v", data, err)
	}
}
