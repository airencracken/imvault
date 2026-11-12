// SPDX-License-Identifier: AGPL-3.0-or-later

package scripts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// pinnedComfylib reports the comfylib release go.mod selects and where the
// module cache holds it.
func pinnedComfylib(t *testing.T) (version, dir string) {
	t.Helper()
	cmd := exec.Command("go", "list", "-m", "-json", comfylibModule)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m %s: %v", comfylibModule, err)
	}
	var module struct{ Version, Dir string }
	if err := json.Unmarshal(out, &module); err != nil {
		t.Fatal(err)
	}
	if module.Dir == "" {
		t.Fatalf("%s %s is not in the module cache; run go mod download", comfylibModule, module.Version)
	}
	return module.Version, module.Dir
}

// The launcher resolves tools from the exact module used to build this app.
func TestMaintenanceLauncherUsesPinnedComfylib(t *testing.T) {
	_, dir := pinnedComfylib(t)
	want, err := os.ReadFile(filepath.Join(dir, "tools", "app_bootstrap.py"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("comfy.py")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("maintenance launcher differs from pinned Comfylib")
	}
}

// mutationSchema is the part of comfylib's tools/mutation-schema.json this
// test enforces: the fields, which are required, and the path patterns.
type mutationSchema struct {
	Items struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type      string `json:"type"`
			MinLength int    `json:"minLength"`
			Pattern   string `json:"pattern"`
		} `json:"properties"`
		AdditionalProperties bool `json:"additionalProperties"`
	} `json:"items"`
	MinItems int `json:"minItems"`
}

// Every table follows the schema of the pinned comfylib's engine, names each
// mutation once across all tables, and anchors each in exactly one place, so
// a table that would be refused at run time fails here first.
func TestMutationTablesFollowComfylibSchema(t *testing.T) {
	_, dir := pinnedComfylib(t)
	raw, err := os.ReadFile(filepath.Join(dir, "tools", "mutation-schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema mutationSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Items.AdditionalProperties || len(schema.Items.Required) == 0 {
		t.Fatalf("comfylib's schema changed shape; update this test: %s", raw)
	}
	tables, err := filepath.Glob("mutations/*.json")
	if err != nil || len(tables) == 0 {
		t.Fatalf("no mutation tables: %v", err)
	}
	names := map[string]string{}
	for _, table := range tables {
		data, err := os.ReadFile(table)
		if err != nil {
			t.Fatal(err)
		}
		var entries []map[string]any
		if err := json.Unmarshal(data, &entries); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if len(entries) < schema.MinItems {
			t.Errorf("%s has %d entries, want at least %d", table, len(entries), schema.MinItems)
		}
		for i, entry := range entries {
			where := table + "[" + strconv.Itoa(i) + "]"
			if name, ok := entry["name"].(string); ok {
				where = table + " (" + name + ")"
				if other, seen := names[name]; seen {
					t.Errorf("%s: name already used in %s", where, other)
				}
				names[name] = table
			}
			checkEntry(t, schema, where, entry)
		}
	}
}

func checkEntry(t *testing.T, schema mutationSchema, where string, entry map[string]any) {
	t.Helper()
	for _, field := range schema.Items.Required {
		if _, ok := entry[field]; !ok {
			t.Errorf("%s: missing %s", where, field)
		}
	}
	for field, value := range entry {
		property, known := schema.Items.Properties[field]
		if !known {
			t.Errorf("%s: unknown field %s", where, field)
			continue
		}
		if property.Type == "object" {
			if _, ok := value.(map[string]any); !ok {
				t.Errorf("%s: %s must be an object", where, field)
			}
			continue
		}
		text, ok := value.(string)
		if !ok {
			t.Errorf("%s: %s must be a string", where, field)
			continue
		}
		if len(text) < property.MinLength {
			t.Errorf("%s: %s is too short", where, field)
		}
		if property.Pattern != "" && !regexp.MustCompile(property.Pattern).MatchString(text) {
			t.Errorf("%s: %s %q does not match %s", where, field, text, property.Pattern)
		}
	}
	file, _ := entry["file"].(string)
	before, _ := entry["before"].(string)
	after, _ := entry["after"].(string)
	if before == after {
		t.Errorf("%s: before and after are the same", where)
	}
	source, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(file)))
	if err != nil {
		t.Errorf("%s: %v", where, err)
		return
	}
	if count := strings.Count(string(source), before); count != 1 {
		t.Errorf("%s: anchor occurs %d times in %s, want exactly once", where, count, file)
	}
	if run, _ := entry["run"].(string); !strings.HasPrefix(run, "^Test") || !strings.HasSuffix(run, "$") {
		t.Errorf("%s: run %q should select one test by its full name", where, run)
	}
}

// Every table is wired to a make target, so none is silently left out of CI.
func TestEveryMutationTableHasAMakeTarget(t *testing.T) {
	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	tables, err := filepath.Glob("mutations/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if !bytes.Contains(makefile, []byte("scripts/"+table)) {
			t.Errorf("no make target runs scripts/%s", table)
		}
	}
	workflow, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"test-sandbox-mutations", "test-invitation-mutations", "test-web-mutations", "test-settings-mutations"} {
		if !slices.Contains(strings.Fields(string(workflow)), target) {
			t.Errorf("CI does not run make %s", target)
		}
	}
}
