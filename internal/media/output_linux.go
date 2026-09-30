// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"os"
	"syscall"
)

func openSandboxOutput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
