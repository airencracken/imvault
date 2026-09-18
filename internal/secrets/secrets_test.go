// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "secret.key"), "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	for _, plaintext := range []string{
		"JBSWY3DPEHPK3PXP",
		"a secret with spaces and \"quotes\"",
		"unicode: ✓ ünïcøde",
		strings.Repeat("x", 4096),
	} {
		encrypted, err := c.Encrypt(plaintext)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		if encrypted == plaintext {
			t.Fatal("the ciphertext is the plaintext")
		}
		if strings.Contains(encrypted, plaintext) {
			t.Fatal("the plaintext is visible in the ciphertext")
		}

		decrypted, err := c.Decrypt(encrypted)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if decrypted != plaintext {
			t.Errorf("round trip gave %q, want %q", decrypted, plaintext)
		}
	}

	// An empty value stays empty rather than becoming a ciphertext.
	empty, err := c.Encrypt("")
	if err != nil || empty != "" {
		t.Errorf("Encrypt(\"\") = %q, %v", empty, err)
	}
	back, err := c.Decrypt("")
	if err != nil || back != "" {
		t.Errorf("Decrypt(\"\") = %q, %v", back, err)
	}
}

func TestEncryptionIsNotDeterministic(t *testing.T) {
	c, _ := Load(filepath.Join(t.TempDir(), "secret.key"), "")

	first, _ := c.Encrypt("same value")
	second, _ := c.Encrypt("same value")

	// The same plaintext must not produce the same ciphertext, or an observer
	// of the database could tell that two accounts share a secret.
	if first == second {
		t.Error("encrypting the same value twice produced identical output")
	}
}

func TestWrongKeyCannotDecrypt(t *testing.T) {
	dir := t.TempDir()

	a, err := Load(filepath.Join(dir, "a.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(filepath.Join(dir, "b.key"), "")
	if err != nil {
		t.Fatal(err)
	}

	encrypted, err := a.Encrypt("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := b.Decrypt(encrypted); !errors.Is(err, ErrNoKey) {
		t.Errorf("decrypting with the wrong key = %v, want ErrNoKey", err)
	}
}

func TestTamperingIsDetected(t *testing.T) {
	c, _ := Load(filepath.Join(t.TempDir(), "secret.key"), "")

	encrypted, err := c.Encrypt("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		t.Fatal(err)
	}

	// Flip a bit in the ciphertext: GCM should refuse it rather than returning
	// something plausible.
	raw[len(raw)-1] ^= 0x01
	if _, err := c.Decrypt(base64.StdEncoding.EncodeToString(raw)); !errors.Is(err, ErrNoKey) {
		t.Errorf("tampered ciphertext = %v, want ErrNoKey", err)
	}
}

func TestMalformedInputIsRejected(t *testing.T) {
	c, _ := Load(filepath.Join(t.TempDir(), "secret.key"), "")

	for _, encoded := range []string{
		"not base64 at all!",
		"YWJj", // valid base64, far too short
	} {
		if _, err := c.Decrypt(encoded); !errors.Is(err, ErrNoKey) {
			t.Errorf("Decrypt(%q) = %v, want ErrNoKey", encoded, err)
		}
	}
}

func TestKeyFileIsCreatedOnceAndReused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "secret.key")

	first, err := Load(path, "")
	if err != nil {
		t.Fatalf("first load: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the key file was not created: %v", err)
	}
	// Only the account running the server should be able to read it.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %o, want 600", perm)
	}

	encrypted, err := first.Encrypt("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}

	// A second Load, as a restart would do, must find the same key.
	second, err := Load(path, "")
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	decrypted, err := second.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("the key changed between loads: %v", err)
	}
	if decrypted != "JBSWY3DPEHPK3PXP" {
		t.Errorf("round trip across loads gave %q", decrypted)
	}
}

func TestExplicitKeyTakesPrecedence(t *testing.T) {
	raw := make([]byte, KeySize)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}

	for label, encoded := range map[string]string{
		"hex":    hex.EncodeToString(raw),
		"base64": base64.StdEncoding.EncodeToString(raw),
	} {
		path := filepath.Join(t.TempDir(), "ignored.key")
		c, err := Load(path, encoded)
		if err != nil {
			t.Fatalf("%s key: %v", label, err)
		}

		encrypted, err := c.Encrypt("value")
		if err != nil {
			t.Fatal(err)
		}

		// A cipher built from the same raw key by the other encoding agrees.
		same, err := newCipher(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := same.Decrypt(encrypted); err != nil || got != "value" {
			t.Errorf("%s: round trip across encodings gave %q, %v", label, got, err)
		}

		// The explicit key means no file is written.
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s: a key file was created even though a key was supplied", label)
		}
	}
}

func TestBadKeysAreRefused(t *testing.T) {
	dir := t.TempDir()

	for label, key := range map[string]string{
		"too short":         "abcd",
		"not hex or base64": "zzzz not a key zzzz",
	} {
		if _, err := Load(filepath.Join(dir, label+".key"), key); err == nil {
			t.Errorf("%s: Load accepted %q", label, key)
		}
	}

	// A key file whose contents are wrong should say so rather than being
	// silently replaced, which would make every stored secret unreadable.
	path := filepath.Join(dir, "corrupt.key")
	if err := os.WriteFile(path, []byte("nonsense\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Error("Load accepted a corrupt key file")
	}
}
