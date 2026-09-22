// SPDX-License-Identifier: AGPL-3.0-or-later

package models

import (
	"crypto/sha256"
	"fmt"
)

func (f *File) ThumbURL() string   { return renditionURL(f.ID, "thumb", f.ThumbKey) }
func (f *File) PreviewURL() string { return renditionURL(f.ID, "preview", f.PreviewKeyOrObject()) }

func renditionURL(id, kind, key string) string {
	hash := sha256.Sum256([]byte(key))
	return fmt.Sprintf("/f/%s/%s?v=%x", id, kind, hash[:8])
}
