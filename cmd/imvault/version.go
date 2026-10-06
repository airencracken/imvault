// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

func printVersion(args []string, out io.Writer) error {
	flags := commandFlags("version", out)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: imvault version")
	}
	_, err := fmt.Fprintf(out, "Imvault %s\n", buildVersion())
	return err
}
