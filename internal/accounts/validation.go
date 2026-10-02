// SPDX-License-Identifier: AGPL-3.0-or-later

// Package accounts shares account validation between web forms and local tools.
package accounts

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

const MinPasswordLength = 8

// MaxPasswordBytes is bcrypt's maximum input length.
const MaxPasswordBytes = 72

func ValidUsername(username string) bool {
	return usernameRE.MatchString(username)
}

func ValidEmail(address string) bool {
	parsed, err := mail.ParseAddress(address)
	return err == nil && parsed.Address == address && strings.Contains(address, ".")
}

// Problem is a validation failure worded as a sentence for the person filling
// in the form, which is why, unlike an ordinary error, it is capitalised and
// punctuated. Callers show it as it is.
type Problem string

func (p Problem) Error() string { return string(p) }

func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return Problem(fmt.Sprintf("Password must be at least %d characters.", MinPasswordLength))
	}
	if len(password) > MaxPasswordBytes {
		return Problem(fmt.Sprintf("Password must be at most %d bytes.", MaxPasswordBytes))
	}
	return nil
}

func ValidateRegistration(username, email, password string) error {
	if !ValidUsername(username) {
		return Problem("Username must be 3-32 characters, using letters, digits, dot, dash or underscore.")
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	if email != "" && !ValidEmail(email) {
		return Problem("That email address does not look valid.")
	}
	return nil
}
