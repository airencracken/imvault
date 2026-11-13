// SPDX-License-Identifier: AGPL-3.0-or-later
package scripts

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestReleaseRaceTimeout(t *testing.T) {
	raw, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	command := regexp.MustCompile(`(?m)^\s+(go test [^\n]*-race[^\n]*) \|\| exit 1$`).FindStringSubmatch(string(raw))
	if len(command) != 2 {
		t.Fatal("release must run the race suite with an explicit test budget")
	}
	flags := strings.Fields(command[1])
	var timeout time.Duration
	race, allPackages := false, false
	for _, flag := range flags {
		if strings.HasPrefix(flag, "-timeout=") {
			timeout, err = time.ParseDuration(strings.TrimPrefix(flag, "-timeout="))
			if err != nil {
				t.Fatal(err)
			}
		}
		race = race || flag == "-race"
		allPackages = allPackages || flag == "./..."
	}
	if timeout < 20*time.Minute || !race || !allPackages {
		t.Fatalf("release must run every race test with at least 20 minutes: %s", command[1])
	}
}

// Keep the Debian gate on a pinned Docker Official Image available without
// Docker Hub pulls, while exercising the complete package on both architectures.
func validDebianLifecycleGate(workflow string) bool {
	image := regexp.MustCompile(`(?m)^\s+public\.ecr\.aws/docker/library/debian:trixie-slim@sha256:[0-9a-f]{64} sh -c '$`)
	if len(image.FindAllString(workflow, -1)) != 1 {
		return false
	}
	for _, required := range []string{"arch: amd64", "arch: arm64", "-e RELEASE_PACKAGE_TEST=1 -e PACKAGE_ARCH", `sh scripts/release/test-deb.sh dist/imvault_*_"$PACKAGE_ARCH".deb`, "rm -f /etc/dpkg/dpkg.cfg.d/docker || exit 1"} {
		if !strings.Contains(workflow, required) {
			return false
		}
	}
	return true
}

func TestReleaseDebianLifecycleContract(t *testing.T) {
	raw, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	if !validDebianLifecycleGate(workflow) {
		t.Fatal("Debian release gate lacks the pinned official mirror or complete package checks")
	}
	for _, mutation := range []struct{ before, after string }{
		{"public.ecr.aws/docker/library/debian:", "debian:"},
		{"public.ecr.aws/docker/library/debian:", "public.ecr.aws/untrusted/debian:"},
		{"trixie-slim@sha256:", "trixie-slim@sha256:x"},
		{"arch: arm64", "arch: other"},
		{"-e RELEASE_PACKAGE_TEST=1 -e PACKAGE_ARCH", "-e PACKAGE_ARCH"},
		{`sh scripts/release/test-deb.sh dist/imvault_*_"$PACKAGE_ARCH".deb`, "true"},
		{"rm -f /etc/dpkg/dpkg.cfg.d/docker || exit 1", "true"},
	} {
		broken := strings.ReplaceAll(workflow, mutation.before, mutation.after)
		if broken == workflow || validDebianLifecycleGate(broken) {
			t.Fatal("unsafe release workflow mutation accepted", mutation.before)
		}
	}
}
