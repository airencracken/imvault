// SPDX-License-Identifier: AGPL-3.0-or-later

// Package suite provides optional navigation between independently hosted apps.
package suite

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"
)

type App struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func (a App) Name() string {
	return map[string]string{"imvault": "Imvault", "songstead": "Songstead", "witmoot": "Witmoot", "folkweave": "Folkweave", "tidings": "Tidings"}[a.ID]
}

// ParseApps accepts a small, explicit operator configuration. It never probes
// destinations or assumes that membership carries between applications.
func ParseApps(raw string) ([]App, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 8192 {
		return nil, errors.New("app navigation configuration exceeds 8 KiB")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var apps []App
	if err := decoder.Decode(&apps); err != nil {
		return nil, errors.New("app navigation needs a JSON array of id and url objects")
	}
	if apps == nil || decoder.Decode(new(any)) != io.EOF || len(apps) > 5 {
		return nil, errors.New("app navigation needs at most five destinations")
	}
	seen := map[string]bool{}
	for _, app := range apps {
		if app.Name() == "" || seen[app.ID] || !validAppURL(app.URL) {
			return nil, errors.New("app navigation contains an unknown, repeated or invalid destination")
		}
		seen[app.ID] = true
	}
	return apps, nil
}

func validAppURL(raw string) bool {
	if len(raw) > 2048 || strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.Contains(u.Host, "\\")
}

func TemplateFuncs(prefix string) (template.FuncMap, error) {
	apps, err := ParseApps(os.Getenv(prefix + "APPS"))
	if err != nil {
		return nil, err
	}
	current := strings.ToLower(strings.TrimSuffix(prefix, "_"))
	var others []App
	for _, app := range apps {
		if app.ID != current {
			others = append(others, app)
		}
	}
	return template.FuncMap{"suiteApps": func() []App { return others }, "sharedSignIn": func() bool { return os.Getenv(prefix+"SHARED_ISSUER") != "" }}, nil
}
