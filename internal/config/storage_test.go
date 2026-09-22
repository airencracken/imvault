// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import "testing"

func TestStorageConfigurationAndIndependentDestination(t *testing.T) {
	t.Setenv("IMVAULT_STORAGE", "s3")
	t.Setenv("IMVAULT_S3_BUCKET", "photos")
	t.Setenv("IMVAULT_S3_ENDPOINT", "https://example.invalid")
	t.Setenv("IMVAULT_S3_REGION", "auto")
	t.Setenv("IMVAULT_S3_PREFIX", "/imvault/")
	t.Setenv("IMVAULT_S3_ACCESS_KEY", "source")
	t.Setenv("IMVAULT_S3_SECRET_KEY", "source-secret")
	t.Setenv("IMVAULT_DEST_STORAGE", "disk")
	t.Setenv("IMVAULT_DEST_OBJECTS_DIR", "/backup/objects")
	source, err := LoadStorage("IMVAULT_", "/data/objects")
	if err != nil || source.Prefix != "imvault" || source.AccessKey != "source" || source.Region != "auto" {
		t.Fatal(source, err)
	}
	dest, err := LoadStorage("IMVAULT_DEST_", "")
	if err != nil || dest.Driver != "disk" || dest.Directory != "/backup/objects" || dest.AccessKey != "" {
		t.Fatal(dest, err)
	}
}

func TestStorageRejectsInvalidSettings(t *testing.T) {
	for key, value := range map[string]string{"STORAGE": "typo", "S3_PATH_STYLE": "maybe", "S3_TIMEOUT": "forever", "S3_ENDPOINT": "https://user:secret@example.com", "S3_PREFIX": "../other", "S3_ACCESS_KEY": "incomplete"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("TEST_STORAGE", "s3")
			t.Setenv("TEST_S3_BUCKET", "photos")
			t.Setenv("TEST_"+key, value)
			if _, err := LoadStorage("TEST_", ""); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
