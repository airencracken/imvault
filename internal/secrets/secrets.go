// SPDX-License-Identifier: AGPL-3.0-or-later

// Package secrets encrypts the small amount of sensitive data that has to be
// readable again later, which in practice means TOTP secrets.
//
// Sessions, API keys and one-time tokens are all stored as digests, because
// the server never needs their plaintext back. A TOTP secret is different: it
// has to be read to check a code, so it is encrypted rather than hashed. The
// key lives in its own file beside the database, so a leak of the database
// alone does not yield usable second factors.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeySize is the AES-256 key length.
const KeySize = 32

// ErrNoKey means the value was encrypted with a different key, or the stored
// text is not something this package produced.
var ErrNoKey = errors.New("secrets: cannot decrypt with this key")

// Cipher encrypts and decrypts short strings with AES-GCM.
type Cipher struct {
	aead cipher.AEAD
}

// Load returns a Cipher backed by the key in keyFile, creating the file with a
// fresh random key if it does not exist yet.
//
// An explicit key (hex or base64, as from an environment variable) takes
// precedence, for deployments that inject secrets rather than storing them on
// disk.
func Load(keyFile, explicitKey string) (*Cipher, error) {
	key, err := keyMaterial(keyFile, explicitKey)
	if err != nil {
		return nil, err
	}
	return newCipher(key)
}

// newCipher builds a Cipher from raw key bytes.
func newCipher(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secrets: key must be %d bytes, got %d", KeySize, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: new GCM: %w", err)
	}

	return &Cipher{aead: aead}, nil
}

// keyMaterial resolves the key, creating the key file when needed.
func keyMaterial(keyFile, explicitKey string) ([]byte, error) {
	if strings.TrimSpace(explicitKey) != "" {
		key, err := decodeKey(explicitKey)
		if err != nil {
			return nil, fmt.Errorf("secrets: IMVAULT_SECRET_KEY: %w", err)
		}
		return key, nil
	}

	if keyFile == "" {
		return nil, errors.New("secrets: no key file or explicit key configured")
	}

	// An existing file is used as-is, however it was created.
	if data, err := os.ReadFile(keyFile); err == nil {
		key, err := decodeKey(string(data))
		if err != nil {
			return nil, fmt.Errorf("secrets: key file %s: %w", keyFile, err)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("secrets: read key file %s: %w", keyFile, err)
	}

	// Otherwise create one, readable only by the account running the server.
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("secrets: generate key: %w", err)
	}

	if dir := filepath.Dir(keyFile); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("secrets: create key directory: %w", err)
		}
	}

	encoded := hex.EncodeToString(key) + "\n"
	if err := os.WriteFile(keyFile, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("secrets: write key file %s: %w", keyFile, err)
	}

	return key, nil
}

// Encrypt returns base64 of the nonce followed by the ciphertext.
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secrets: nonce: %w", err)
	}

	// The nonce is prepended rather than stored alongside, so a value is
	// self-contained and cannot be paired with the wrong one.
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt.
func (c *Cipher) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("%w: not base64", ErrNoKey)
	}
	if len(raw) < c.aead.NonceSize() {
		return "", fmt.Errorf("%w: too short", ErrNoKey)
	}

	nonce, ciphertext := raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// GCM authentication failed: wrong key, or the text was altered.
		return "", ErrNoKey
	}
	return string(plaintext), nil
}

// decodeKey accepts hex or base64, so a key can be pasted from either.
func decodeKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)

	if key, err := hex.DecodeString(value); err == nil && len(key) == KeySize {
		return key, nil
	}
	if key, err := base64.StdEncoding.DecodeString(value); err == nil && len(key) == KeySize {
		return key, nil
	}
	return nil, fmt.Errorf("expected %d bytes encoded as hex or base64", KeySize)
}
