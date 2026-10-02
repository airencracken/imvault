// SPDX-License-Identifier: AGPL-3.0-or-later

package accounts

import (
	"errors"
	"strings"
	"testing"
)

func TestUsernames(t *testing.T) {
	for _, ok := range []string{"abc", "Marcus", "a.b-c_d", strings.Repeat("x", 32)} {
		if !ValidUsername(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "ab", strings.Repeat("x", 33), "has space", "../x", "名前", "a\x00b", "~", "a@b"} {
		if ValidUsername(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestEmails(t *testing.T) {
	for _, ok := range []string{"a@example.com", "first.last+tag@sub.example.org"} {
		if !ValidEmail(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "a@localhost", "Name <a@example.com>", "a@example.com\r\nBcc: b@example.com", " a@example.com"} {
		if ValidEmail(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPasswordsAndRegistrationReportProblems(t *testing.T) {
	for password, ok := range map[string]bool{
		"short":                 false,
		"long enough":           true,
		strings.Repeat("é", 8):  true,  // eight characters, sixteen bytes
		strings.Repeat("x", 72): true,  // bcrypt's limit
		strings.Repeat("x", 73): false, // past it, and silently truncated
		strings.Repeat("€", 25): false, // 25 characters, 75 bytes
	} {
		err := ValidatePassword(password)
		if (err == nil) != ok {
			t.Errorf("password of %d bytes: %v", len(password), err)
		}
		var problem Problem
		if err != nil && !errors.As(err, &problem) {
			t.Errorf("%v is not a Problem the form can show", err)
		}
	}
	if err := ValidateRegistration("ok_name", "", "long enough"); err != nil {
		t.Errorf("an address is optional: %v", err)
	}
	if err := ValidateRegistration("ok_name", "not-an-address", "long enough"); err == nil {
		t.Error("a bad address was accepted")
	}
}
