// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"errors"
	"fmt"
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
	pages    map[string]*template.Template
	partials *template.Template
}

func newRenderer() (*renderer, error) {
	funcs := templateFuncs()

	pageFiles, err := fs.Glob(templatesFS, "templates/pages/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: glob pages: %w", err)
	}
	if len(pageFiles) == 0 {
		return nil, errors.New("web: no page templates found")
	}

	r := &renderer{pages: make(map[string]*template.Template, len(pageFiles))}

	// Build the standalone partial set used for HTMX responses.
	r.partials, err = template.New("partials").Funcs(funcs).
		ParseFS(templatesFS, "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse partials: %w", err)
	}

	for _, file := range pageFiles {
		name := strings.TrimSuffix(path.Base(file), ".html")
		t, err := template.New(name).Funcs(funcs).
			ParseFS(templatesFS, "templates/layout.html", "templates/partials/*.html", file)
		if err != nil {
			return nil, fmt.Errorf("web: parse page %s: %w", name, err)
		}
		r.pages[name] = t
	}

	return r, nil
}

// page renders a full document (layout + page content).
func (r *renderer) page(w io.Writer, name string, data any) error {
	t, ok := r.pages[name]
	if !ok {
		return fmt.Errorf("web: unknown page template %q", name)
	}
	return t.ExecuteTemplate(w, "layout", data)
}

// partial renders a named fragment for an HTMX swap.
func (r *renderer) partial(w io.Writer, name string, data any) error {
	if r.partials.Lookup(name) == nil {
		return fmt.Errorf("web: unknown partial template %q", name)
	}
	return r.partials.ExecuteTemplate(w, name, data)
}

// renderPartial writes a fragment, buffering first so a template error cannot
// emit a half-written 200 response.
func (s *Server) renderPartial(w http.ResponseWriter, name string, data any) {
	s.renderPartialStatus(w, http.StatusOK, name, data)
}

// renderPartialStatus writes a fragment under a chosen status.
//
// A fragment is not always an answer to something that worked: a busy server
// returns a 503 that still carries something worth showing. Sending 200 to mean
// "no" would hide overload from anything watching status codes.
func (s *Server) renderPartialStatus(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.render.partial(&buf, name, data); err != nil {
		s.log.Error("render partial", "template", name, "error", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

// renderPage writes a full page, buffering first for the same reason.
func (s *Server) renderPage(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.render.page(&buf, name, data); err != nil {
		s.log.Error("render page", "template", name, "error", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

// templateFuncs returns the helpers exposed to templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"humanSize": models.HumanSize,
		"humanTime": models.HumanTime,
		"date": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format("2 Jan 2006, 15:04")
		},
		"slug":   ids.Slug,
		"add":    func(a, b int) int { return a + b },
		"sub":    func(a, b int) int { return a - b },
		"div":    func(a, b int64) int64 { return a / b },
		"hasTag": hasTag,
		"join":   strings.Join,
		"lower":  strings.ToLower,
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
				File:       f,
				CSRFToken:  view.CSRFToken,
				Removable:  removable,
				Selectable: view.Selectable,
				ShowOwner:  view.ShowOwner,
				RemoveURL:  removeURL,
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
