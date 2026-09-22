// SPDX-License-Identifier: AGPL-3.0-or-later

package models

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxFileDescription = 1000

// NormalizeFileDescription keeps plain text and line breaks, allowing an empty
// value to clear a description. Limits count characters, not UTF-8 bytes.
func NormalizeFileDescription(raw string) (string, error) {
	text := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if !utf8.ValidString(text) || strings.IndexFunc(text, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t'
	}) >= 0 {
		return "", errors.New("description must be plain text without control characters")
	}
	if utf8.RuneCountInString(text) > MaxFileDescription {
		return "", errors.New("description must be 1,000 characters or fewer")
	}
	return text, nil
}
