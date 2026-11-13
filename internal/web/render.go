// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/airencracken/comfylib/admin"
	"github.com/airencracken/comfylib/displaytime"
	"github.com/airencracken/comfylib/suite"
	"github.com/airencracken/comfylib/ui"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"imvault/internal/ids"
	"imvault/internal/models"
)

// renderer holds two template sets:
//
//   - pages: layout + all partials + one page template, executed as "layout"
//   - partials: every partial, executed by its own define name for HTMX swaps
//
// Partials are parsed into every page set so pages can embed the same
// fragments that HTMX swaps in, keeping markup in exactly one place.
type renderer struct {
	pages    map[string]*displaytime.Templates
	partials *displaytime.Templates
}

func newRenderer() (*renderer, error) {
	suiteFuncs, suiteErr := suite.TemplateFuncs("IMVAULT_")
	if suiteErr != nil {
		return nil, suiteErr
	}
	funcs := templateFuncs()
	for k, v := range displaytime.New("UTC").Funcs() {
		funcs[k] = v
	}
	funcs["adminSummary"] = admin.Render
	for key, value := range suiteFuncs {
		funcs[key] = value
	}

	pageFiles, err := fs.Glob(templatesFS, "templates/pages/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: glob pages: %w", err)
	}
	if len(pageFiles) == 0 {
		return nil, errors.New("web: no page templates found")
	}

	r := &renderer{pages: make(map[string]*displaytime.Templates, len(pageFiles))}

	// Build the standalone partial set used for HTMX responses.
	parsedPartials, err := template.New("partials").Funcs(funcs).
		ParseFS(templatesFS, "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse partials: %w", err)
	}

	r.partials, err = displaytime.NewTemplates(parsedPartials, displayFuncs)
	if err != nil {
		return nil, err
	}
	for _, file := range pageFiles {
		name := strings.TrimSuffix(path.Base(file), ".html")
		t, err := template.New(name).Funcs(funcs).
			ParseFS(templatesFS, "templates/layout.html", "templates/partials/*.html", file)
		if err != nil {
			return nil, fmt.Errorf("web: parse page %s: %w", name, err)
		}
		r.pages[name], err = displaytime.NewTemplates(t, displayFuncs)
		if err != nil {
			return nil, err
		}
	}

	return r, nil
}

// page renders a full document (layout + page content).
func (r *renderer) page(w io.Writer, name string, data any, zone ...string) error {
	t, ok := r.pages[name]
	if !ok {
		return fmt.Errorf("web: unknown page template %q", name)
	}
	return t.ExecuteTemplate(w, timezoneArg(zone), "layout", data)
}

// partial renders a named fragment for an HTMX swap.
func (r *renderer) partial(w io.Writer, name string, data any, zone ...string) error {
	if !r.partials.Has(name) {
		return fmt.Errorf("web: unknown partial template %q", name)
	}
	return r.partials.ExecuteTemplate(w, timezoneArg(zone), name, data)
}

// renderPartial writes a fragment, buffering first so a template error cannot
// emit a half-written 200 response.
func (s *Server) renderPartial(w http.ResponseWriter, r *http.Request, name string, data any) {
	s.renderPartialStatus(w, r, http.StatusOK, name, data)
}

// renderPartialStatus writes a fragment under a chosen status.
//
// A fragment is not always an answer to something that worked: a busy server
// returns a 503 that still carries something worth showing. Sending 200 to mean
// "no" would hide overload from anything watching status codes.
func (s *Server) renderPartialStatus(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.render.partial(&buf, name, data, preferredTimezone(r)); err != nil {
		s.log.Error("render partial", "template", name, "error", err)
		http.Error(w, "Something went wrong on our side. Please try again.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	s.writeBody(w, buf.Bytes())
}

// renderPage writes a full page, buffering first for the same reason.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.render.page(&buf, name, data, preferredTimezone(r)); err != nil {
		s.log.Error("render page", "template", name, "error", err)
		http.Error(w, "Something went wrong on our side. Please try again.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	s.writeBody(w, buf.Bytes())
}

// templateFuncs returns the helpers exposed to templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{"timezonePreference": displaytime.RenderPreference, "linkNumber": func(i int) int { return i + 1 },
		"months": func() []struct{ Value, Label string } {
			out := make([]struct{ Value, Label string }, 12)
			for i := range out {
				out[i].Value = fmt.Sprintf("%02d", i+1)
				out[i].Label = time.Month(i + 1).String()
			}
			return out
		},
		"humanSize":   ui.Size,
		"themePicker": func() template.HTML { return ui.ThemePicker(true) },
		"palettes":    ui.Palettes,
		"humanTime":   models.HumanTime,
		"date":        displaytime.New("UTC").Stamp,
		"slug":        ids.Slug,
		"add":         func(a, b int) int { return a + b },
		"sub":         func(a, b int) int { return a - b },
		"div":         func(a, b int64) int64 { return a / b },
		"hasTag":      hasTag,
		"join":        strings.Join,
		"lower":       strings.ToLower,
		"card": func(view fileCardsView, f *models.File) fileCard {
			// A tile is removable if the whole grid is, or if this particular
			// file belongs to the account being allowed to take its own back.
			removable := view.Removable
			if !removable && view.RemovableOwner != nil && f.UserID != nil &&
				*f.UserID == *view.RemovableOwner {
				removable = true
			}

			removeURL := ""
			if removable {
				pattern := view.RemovePattern
				if pattern == "" {
					pattern = "/f/%s/delete"
				}
				removeURL = strings.Replace(pattern, "%s", f.ID, 1)
			}
			return fileCard{
				ShowTaken:     view.ShowTaken,
				SelectionForm: view.SelectionForm,
				File:          f,
				CSRFToken:     view.CSRFToken,
				Removable:     removable,
				Selectable:    view.Selectable,
				ShowOwner:     view.ShowOwner,
				RemoveURL:     removeURL,
			}
		},
		"default": func(fallback, v string) string {
			if strings.TrimSpace(v) == "" {
				return fallback
			}
			return v
		},
	}
}

func hasTag(tags []models.Tag, slug string) bool {
	for _, t := range tags {
		if t.Slug == slug {
			return true
		}
	}
	return false
}

func timezoneArg(zones []string) string {
	if len(zones) > 0 {
		return zones[0]
	}
	return "UTC"
}
func displayFuncs(f displaytime.Formatter) template.FuncMap {
	m := f.Funcs()
	m["humanTime"] = func(t time.Time) string {
		d := time.Since(t)
		if d < 0 {
			d = -d
		}
		if d >= 30*24*time.Hour {
			return f.Date(t)
		}
		return models.HumanTime(t)
	}
	return m
}
func preferredTimezone(r *http.Request) string {
	if r == nil {
		return "UTC"
	}
	if u := currentUser(r.Context()); u != nil {
		return displaytime.New(u.Timezone).Name()
	}
	return "UTC"
}
