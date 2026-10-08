// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"io"

	"github.com/airencracken/comfylib/password"
	"os"

	"golang.org/x/term"
)

func readPromptAdminPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("password prompt requires a terminal; use --password-stdin")
	}
	return readConfirmedAdminPassword(os.Stderr, func() ([]byte, error) {
		return term.ReadPassword(fd)
	})
}

func readConfirmedAdminPassword(out io.Writer, read func() ([]byte, error)) (string, error) {
	return password.Confirm(out, "Administrator password: ", "Confirm administrator password: ", read)
}
