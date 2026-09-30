// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build !linux

package media

import (
	"errors"
	"os"
)

func openSandboxOutput(path string) (*os.File, error) {
	return nil, errors.New("Bubblewrap requires Linux")
}
