// SPDX-License-Identifier: AGPL-3.0-or-later
package scripts

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

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
