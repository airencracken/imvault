// SPDX-License-Identifier: AGPL-3.0-or-later
package suite

import (
	"strings"
	"testing"
	"testing/quick"
)

func TestAppsConfigurationContract(t *testing.T) {
	apps, err := ParseApps(`[{"id":"imvault","url":"https://photos.example.org/"},{"id":"witmoot","url":"https://family.example.org/board"}]`)
	if err != nil || len(apps) != 2 || apps[0].Name() != "Imvault" {
		t.Fatal(apps, err)
	}
	for _, raw := range []string{`[{"id":"imvault","url":"https://example.org:65536"}]`, `[{"id":"imvault","url":"https://example.org:0"}]`, `null`, `{}`, `[] []`, `[{"id":"other","url":"https://example.org"}]`, `[{"id":"imvault","url":"javascript:alert(1)"}]`, `[{"id":"imvault","url":"https://user:password@example.org"}]`, `[{"id":"imvault","url":"https://example.org?token=secret"}]`, `[{"id":"imvault","url":"https://example.org","extra":1}]`, `[{"id":"imvault","url":"https://example.org"},{"id":"imvault","url":"https://example.net"}]`, strings.Repeat(" ", 8193)} {
		if _, err := ParseApps(raw); err == nil {
			t.Fatal("unsafe or malformed configuration accepted")
		}
	}
	t.Setenv("IMVAULT_APPS", `[{"id":"imvault","url":"https://photos.example.org"},{"id":"songstead","url":"https://music.example.org"}]`)
	funcs, err := TemplateFuncs("IMVAULT_")
	if err != nil {
		t.Fatal(err)
	}
	others := funcs["suiteApps"].(func() []App)()
	if len(others) != 1 || others[0].ID != "songstead" {
		t.Fatal(others)
	}
}

func TestAppsRejectArbitrarySchemes(t *testing.T) {
	if err := quick.Check(func(raw string) bool {
		if !validAppURL(raw) {
			return true
		}
		return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
	}, nil); err != nil {
		t.Fatal(err)
	}
}

func FuzzParseApps(f *testing.F) {
	f.Add(`[{"id":"tidings","url":"https://news.example.org"}]`)
	f.Fuzz(func(t *testing.T, raw string) {
		apps, err := ParseApps(raw)
		if err == nil && len(apps) > 5 {
			t.Fatal("unbounded app configuration")
		}
	})
}
