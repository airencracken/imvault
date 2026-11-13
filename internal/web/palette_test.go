// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"regexp"
	"strings"
	"testing"
)

func appStylesheet(t *testing.T) string {
	t.Helper()
	source, err := staticFS.ReadFile("static/css/app.css")
	if err != nil {
		t.Fatal(err)
	}
	return string(source)
}

// Every colour lives in the token block at the top, so the palette can be
// swapped for Comfylib's shared tokens in one place.
func TestEveryColourComesFromTheTokenBlock(t *testing.T) {
	css := appStylesheet(t)
	end := strings.Index(css, "* { box-sizing: border-box; }")
	if end < 0 {
		t.Fatal("cannot find the end of the token block")
	}
	literal := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(|light-dark\(`)
	for i, line := range strings.Split(css[end:], "\n") {
		if literal.MatchString(line) {
			t.Errorf("colour outside the token block, line %d after it: %s", i+1, strings.TrimSpace(line))
		}
	}
	tokens := css[:end]
	for _, name := range []string{"--bg", "--bg-elev", "--bg-elev-2", "--border", "--text", "--muted", "--accent", "--accent-fg", "--danger", "--ok", "--warn"} {
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:`).MatchString(tokens) {
			t.Errorf("token %s is not defined in the block", name)
		}
	}
}

// The shared admin, invitation and metrics pages read --comfy-* tokens; they
// must resolve to Imvault's own, with a selector that outranks the defaults in
// components.css.
func TestSharedComfyTokensMapToImvaultTokens(t *testing.T) {
	css := appStylesheet(t)
	block := regexp.MustCompile(`(?s)html\[data-comfy-app="imvault"\],\s*html\[data-comfy-app="imvault"\] \.comfy-admin-dashboard \{(.*?)\}`).FindStringSubmatch(css)
	if block == nil {
		t.Fatal("no --comfy-* mapping for the Imvault document and the admin dashboard")
	}
	for token, source := range map[string]string{
		"--comfy-ink": "var(--text)", "--comfy-panel": "var(--bg-elev)", "--comfy-line": "var(--border)",
		"--comfy-accent": "var(--accent)", "--comfy-admin-muted": "var(--muted)", "--comfy-radius": "var(--radius)",
	} {
		if !strings.Contains(block[1], token+": "+source+";") {
			t.Errorf("%s does not map to %s", token, source)
		}
	}
	layout := readTemplate(t, "layout.html")
	if !strings.Contains(layout, `data-comfy-app="imvault"`) {
		t.Error("the layout no longer carries the attribute the mapping relies on")
	}
	if strings.Index(layout, "/static/css/app.css") > strings.Index(layout, "/static/comfyware/components.css") {
		t.Log("app.css loads after components.css; the attribute selector still wins")
	}
}
