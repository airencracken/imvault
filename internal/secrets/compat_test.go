// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures in testdata hold one key in each form an instance may already
// have: hex with a newline, as every earlier release wrote the key file, and
// base64, as an operator may have pasted it. totp.sealed was encrypted with
// that key by the release before key files moved to comfylib.
const fixtureSecret = "JBSWY3DPEHPK3PXP"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// copyFixture places a fixture where Load may create files beside it, so a
// regression cannot change the checked-in copy.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(path, readFixture(t, name), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExistingKeyFilesKeepDecryptingStoredSecrets(t *testing.T) {
	sealed := strings.TrimSpace(string(readFixture(t, "totp.sealed")))
	for _, name := range []string{"hex.key", "base64.key"} {
		t.Run(name, func(t *testing.T) {
			path := copyFixture(t, name)
			c, err := Load(path, "")
			if err != nil {
				t.Fatalf("an existing %s no longer loads: %v", name, err)
			}
			if got, err := c.Decrypt(sealed); err != nil || got != fixtureSecret {
				t.Fatalf("Decrypt = %q, %v; want the stored secret", got, err)
			}
			// Loading reads the file; it never rewrites it in another form.
			if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, readFixture(t, name)) {
				t.Fatalf("the key file changed on load: %q %v", after, err)
			}
			if entries, err := os.ReadDir(filepath.Dir(path)); err != nil || len(entries) != 1 {
				t.Fatalf("loading left other files beside the key: %v %v", entries, err)
			}
		})
	}
}

func TestExistingSecretKeyValuesKeepDecryptingStoredSecrets(t *testing.T) {
	sealed := strings.TrimSpace(string(readFixture(t, "totp.sealed")))
	hexKey := strings.TrimSpace(string(readFixture(t, "hex.key")))
	base64Key := strings.TrimSpace(string(readFixture(t, "base64.key")))
	for name, value := range map[string]string{
		"hex":                 hexKey,
		"base64":              base64Key,
		"hex with newline":    hexKey + "\n",
		"base64 with spacing": "  " + base64Key + "\r\n",
		"upper-case hex":      strings.ToUpper(hexKey),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unused.key")
			c, err := Load(path, value)
			if err != nil {
				t.Fatalf("IMVAULT_SECRET_KEY in %s form no longer loads: %v", name, err)
			}
			if got, err := c.Decrypt(sealed); err != nil || got != fixtureSecret {
				t.Fatalf("Decrypt = %q, %v; want the stored secret", got, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("a key file was written although the key came from the environment")
			}
		})
	}
}

// A new key file must be in the form earlier releases read, so a downgrade
// still finds its key.
func TestNewKeyFilesAreWrittenAsHex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	if _, err := Load(path, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if len(text) != 2*KeySize+1 || !strings.HasSuffix(text, "\n") || strings.Trim(text[:2*KeySize], "0123456789abcdef") != "" {
		t.Fatalf("new key file is not lowercase hex and a newline: %q", text)
	}
}

func TestAdversarialSecretKeyValuesAreRefused(t *testing.T) {
	hexKey := strings.TrimSpace(string(readFixture(t, "hex.key")))
	for name, value := range map[string]string{
		"one byte short":   hexKey[:len(hexKey)-2],
		"one byte long":    hexKey + "00",
		"raw bytes":        string(bytes.Repeat([]byte{0x41}, KeySize)),
		"URL-safe base64":  "AwoRGB8mLTQ7QklQV15lbHN6gYiPlp2kq7K5wMfO1dw",
		"embedded newline": hexKey[:32] + "\n" + hexKey[32:],
		"NUL":              hexKey + "\x00",
	} {
		if _, err := Load(filepath.Join(t.TempDir(), "unused.key"), value); err == nil {
			t.Errorf("%s was accepted as IMVAULT_SECRET_KEY", name)
		}
	}
}
