// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"regexp"
	"testing"
	"time"
)

// sandboxStopWait is how long the Bubblewrap launcher waits for the server
// after forwarding SIGTERM (internal/sandbox/run.go).
const sandboxStopWait = 20 * time.Second

// The init systems must wait longer than the server takes to stop, and longer
// than the sandbox launcher waits for it, or a stop kills requests the server
// was still allowed to finish.
func TestServiceDefinitionsOutwaitTheGracefulShutdown(t *testing.T) {
	for _, tc := range []struct {
		file    string
		pattern string
	}{
		{"../../contrib/systemd/imvault.service", `(?m)^TimeoutStopSec=(\d+)s$`},
		{"../../contrib/openrc/imvault", `(?m)^retry="TERM/(\d+)/KILL/\d+"$`},
	} {
		raw, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		matches := regexp.MustCompile(tc.pattern).FindAllStringSubmatch(string(raw), -1)
		if len(matches) != 1 {
			t.Fatalf("%s: want one stop timeout, found %d", tc.file, len(matches))
		}
		seconds, err := time.ParseDuration(matches[0][1] + "s")
		if err != nil {
			t.Fatal(err)
		}
		if seconds <= shutdownGrace || seconds <= sandboxStopWait {
			t.Errorf("%s waits %s; the server drains for %s and the sandbox launcher waits %s",
				tc.file, seconds, shutdownGrace, sandboxStopWait)
		}
	}
}
