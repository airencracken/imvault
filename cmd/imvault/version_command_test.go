// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingVersionWriter struct{}

func (failingVersionWriter) Write([]byte) (int, error) { return 0, errors.New("closed output") }

func TestVersionCommandIsExactAndDoesNotTouchData(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	directory := filepath.Join(t.TempDir(), "must-not-exist")
	t.Setenv("IMVAULT_DATA_DIR", directory)
	t.Setenv("IMVAULT_SECURE_COOKIES", "invalid-on-purpose")
	t.Setenv("IMVAULT_STORAGE", "invalid-on-purpose")
	for _, stamp := range []string{"devel", "0.15.0", "v0.15.0", "v0.15.0-2-gabc123"} {
		version = stamp
		for _, args := range [][]string{{"--version"}, {"version"}} {
			var out bytes.Buffer
			if err := runCommand(args, nil, &out); err != nil || out.String() != "Imvault "+stamp+"\n" {
				t.Fatalf("%v: %q %v", args, out.String(), err)
			}
		}
	}
	for _, args := range [][]string{{"version", "--help"}, {"help", "version"}, {"version", "-h"}} {
		var out bytes.Buffer
		if err := runCommand(args, nil, &out); err != nil || !strings.Contains(out.String(), "Usage: imvault version") {
			t.Fatalf("version help: %q %v", out.String(), err)
		}
	}
	for _, args := range [][]string{{"--version", "extra"}, {"--version", "--help"}, {"version", "extra"}, {"version", "--unknown"}, {"version", "../data"}} {
		if err := runCommand(args, nil, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
	if err := runCommand([]string{"--version"}, nil, failingVersionWriter{}); err == nil {
		t.Fatal("ignored output error")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("version opened data: %v", err)
	}
	version = ""
	if buildVersion() != "devel" {
		t.Fatal("unstamped build did not report devel")
	}
}
