// SPDX-License-Identifier: AGPL-3.0-or-later

// Package accounts shares account validation between web forms and local tools.
package accounts

import (
	"net/mail"
	"regexp"
	"strings"

	"github.com/airencracken/comfylib/password"
)

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

// MaxPasswordBytes is bcrypt's maximum input length.
const MaxPasswordBytes = password.MaxBytes

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

// ValidatePassword applies the shared Comfyware rule for a new password. Its
// errors are plain sentences, shown as they are.
func ValidatePassword(value string) error {
	if err := password.Validate(value); err != nil {
		return Problem(err.Error())
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
