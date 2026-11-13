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

// Imvault's names read the shared --comfy-* tokens, so Comfylib's palettes
// recolour Imvault's pages as well as the shared ones, and the layout carries
// the site palette and the old theme storage key.
func TestImvaultTokensReadTheSharedTokens(t *testing.T) {
	css := appStylesheet(t)
	for token, source := range map[string]string{
		"--bg": "var(--comfy-surface)", "--bg-elev": "var(--comfy-raised)", "--border": "var(--comfy-border)",
		"--text": "var(--comfy-text)", "--muted": "var(--comfy-muted)", "--accent": "var(--comfy-accent)",
		"--accent-fg": "var(--comfy-accent-text)", "--danger": "var(--comfy-danger)", "--radius": "var(--comfy-radius)",
	} {
		if !strings.Contains(css, token+": "+source+";") {
			t.Errorf("%s does not read %s", token, source)
		}
	}
	for _, shared := range []string{"--comfy-surface:", "--comfy-raised:", "--comfy-text:", "--comfy-accent:", "--comfy-control-border:"} {
		if !strings.Contains(css, shared) {
			t.Errorf("no slate fallback for %s", shared)
		}
	}
	for _, retired := range []string{"--comfy-ink", "--comfy-panel", "--comfy-line", ".theme-picker {"} {
		if strings.Contains(css, retired) {
			t.Errorf("app.css still defines %s", retired)
		}
	}
	layout := readTemplate(t, "layout.html")
	for _, want := range []string{`data-theme-key="imvault-theme"`, "{{.PaletteAttrs}}", `<script src="/static/comfyware/theme.js"></script>`, "/static/comfyware/palettes.css"} {
		if !strings.Contains(layout, want) {
			t.Errorf("layout is missing %s", want)
		}
	}
	if strings.Index(layout, "/static/comfyware/palettes.css") < strings.Index(layout, "/static/css/app.css") {
		t.Error("palettes.css must load after app.css")
	}
}
