// SPDX-License-Identifier: AGPL-3.0-or-later

package scripts_test

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// comfylibModule is the shared library every release must fetch through the
// module proxy, at the version go.mod names.
const comfylibModule = "github.com/airencracken/comfylib"

// releaseVersion matches a tagged semantic version, not a pseudo-version or a
// local path.
var releaseVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// goModDirectives returns go.mod's directives with comments removed, each
// prefixed by its verb, so "replace (\n a => b\n)" yields "replace a => b".
func goModDirectives(t *testing.T, contents string) []string {
	t.Helper()
	var directives []string
	block := ""
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		line := scanner.Text()
		if comment := strings.Index(line, "//"); comment >= 0 {
			line = line[:comment]
		}
		fields := strings.Fields(line)
		switch {
		case len(fields) == 0:
		case block != "" && fields[0] == ")":
			block = ""
		case block != "":
			directives = append(directives, block+" "+strings.Join(fields, " "))
		case len(fields) == 2 && fields[1] == "(":
			block = fields[0]
		default:
			directives = append(directives, strings.Join(fields, " "))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return directives
}

// moduleProblems reports what in go.mod would make a build differ from the
// one the module proxy, the Gentoo bundle and the container image produce.
func moduleProblems(t *testing.T, contents string) []string {
	t.Helper()
	var problems []string
	pinned := false
	for _, directive := range goModDirectives(t, contents) {
		fields := strings.Fields(directive)
		switch fields[0] {
		case "replace":
			problems = append(problems, "go.mod must not carry a replace directive: "+directive)
		case "require":
			if len(fields) >= 3 && fields[1] == comfylibModule {
				pinned = true
				if !releaseVersion.MatchString(fields[2]) {
					problems = append(problems, "comfylib must be pinned to a tagged release, not "+fields[2])
				}
			}
		}
	}
	if !pinned {
		problems = append(problems, "go.mod does not require "+comfylibModule)
	}
	return problems
}

func TestGoModPinsComfylibWithoutReplacements(t *testing.T) {
	contents, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range moduleProblems(t, string(contents)) {
		t.Error(problem)
	}
}

// The checker itself must notice each way a pin can go wrong, or the test
// above proves nothing.
func TestModuleCheckRejectsReplacementsAndUnpinnedVersions(t *testing.T) {
	good := "module imvault\n\nrequire (\n\t" + comfylibModule + " v0.1.0\n)\n"
	if problems := moduleProblems(t, good); len(problems) != 0 {
		t.Fatalf("a clean go.mod was refused: %v", problems)
	}
	for name, contents := range map[string]string{
		"single replace":    good + "replace " + comfylibModule + " => ../comfylib\n",
		"replace block":     good + "replace (\n\t" + comfylibModule + " v0.1.0 => ../comfylib\n)\n",
		"indented replace":  good + "  replace example.org/x => example.org/y v1.0.0 // fork\n",
		"pseudo-version":    "module imvault\n\nrequire " + comfylibModule + " v0.1.1-0.20261002120000-abcdefabcdef\n",
		"local version tag": "module imvault\n\nrequire " + comfylibModule + " v0.1.0+dirty\n",
		"missing":           "module imvault\n",
	} {
		if problems := moduleProblems(t, contents); len(problems) == 0 {
			t.Errorf("%s was accepted", name)
		}
	}
	// A replace mentioned only in a comment is not a directive.
	if problems := moduleProblems(t, good+"// replace "+comfylibModule+" => ../comfylib\n"); len(problems) != 0 {
		t.Errorf("a comment was read as a directive: %v", problems)
	}
}

// A go.work points builds at other checkouts. It belongs to one developer's
// worktree and must never reach the repository or a release tarball.
func TestNoWorkspaceFileIsTracked(t *testing.T) {
	for _, name := range []string{"go.work", "go.work.sum"} {
		ignored, err := os.ReadFile("../.gitignore")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains("\n"+string(ignored), "\n/"+name+"\n") {
			t.Errorf(".gitignore does not ignore /%s", name)
		}
		docker, err := os.ReadFile("../.dockerignore")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains("\n"+string(docker), "\n"+name+"\n") {
			t.Errorf(".dockerignore does not leave out %s", name)
		}
	}
	git, err := exec.LookPath("git")
	if err == nil {
		out, err := exec.Command(git, "-C", "..", "ls-files", "--", "go.work", "go.work.sum").Output()
		if err == nil {
			if tracked := strings.TrimSpace(string(out)); tracked != "" {
				t.Fatalf("workspace files are tracked: %s", tracked)
			}
			return
		}
	}
	// Outside a git checkout, such as an unpacked release, the files must
	// simply be absent.
	for _, name := range []string{"../go.work", "../go.work.sum"} {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is present outside a git checkout: %v", name, err)
		}
	}
}
