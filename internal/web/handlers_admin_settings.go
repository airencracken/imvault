// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"imvault/internal/models"
	"imvault/internal/store"
)

// settingSource says where a value came from, so the admin page can be explicit
// about it rather than leaving an operator to guess whether the environment or
// the interface is in charge.
type settingSource struct {
	Values models.Settings
	// Stored marks the settings an administrator has set here. Anything else is
	// coming from the configuration.
	Stored map[string]bool
}

// installSettings loads the policy at startup and keeps it in memory.
//
// The values are read on nearly every request — the navigation shows whether
// signup is open, uploads need the retention window — so they are cached and
// replaced when an administrator saves. Nothing else writes them, so the cache
// cannot go stale behind the server's back.
func (s *Server) installSettings(ctx context.Context) error {
	if err := s.reloadSettings(ctx); err != nil {
		return err
	}
	return nil
}

// reloadSettings refreshes the cache from the database and configuration.
func (s *Server) reloadSettings(ctx context.Context) error {
	defaults := models.Settings{
		AllowSignup:           s.cfg.AllowSignup,
		AllowAnonymousUploads: s.cfg.AllowAnonymousUploads,
		AnonymousTTL:          s.cfg.AnonymousTTL,
	}

	values, stored, err := s.store.LoadSettings(ctx, defaults)
	if err != nil {
		return err
	}

	s.settings.Store(&settingSource{Values: values, Stored: stored})
	return nil
}

// policy is the current instance policy.
func (s *Server) policy() models.Settings {
	if current := s.settings.Load(); current != nil {
		return current.Values
	}
	// Only reachable if the server was constructed without loading, which
	// should not happen; the configuration is the safe answer.
	return models.Settings{
		AllowSignup:           s.cfg.AllowSignup,
		AllowAnonymousUploads: s.cfg.AllowAnonymousUploads,
		AnonymousTTL:          s.cfg.AnonymousTTL,
	}
}

// settingSources reports which settings are currently overridden.
func (s *Server) settingSources() map[string]bool {
	if current := s.settings.Load(); current != nil && current.Stored != nil {
		return current.Stored
	}
	return map[string]bool{}
}

// settingsView backs the instance settings page.
type settingsView struct {
	base
	AllowSignup           bool
	AllowAnonymousUploads bool
	// Retention is the window as written for a person, for example "24h" or
	// "7d0h".
	Retention string
	// Stored marks which of the three are set here rather than in the
	// environment.
	Stored map[string]bool
	// Config values, shown so an operator can see what clearing would restore.
	ConfigSignup     bool
	ConfigAnon       bool
	ConfigRetention  string
	AnonymousPending int
	Error            string
	Notice           string
}

// handleAdminSettings shows the instance-wide policy.
func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	policy := s.policy()

	pending, err := s.store.CountFiles(r.Context(), fileQueryAnonymous())
	if err != nil {
		s.log.Error("admin settings: count anonymous", "error", err)
	}

	view := settingsView{
		AllowSignup:           policy.AllowSignup,
		AllowAnonymousUploads: policy.AllowAnonymousUploads,
		Retention:             formatRetention(policy.AnonymousTTL),
		Stored:                s.settingSources(),
		ConfigSignup:          s.cfg.AllowSignup,
		ConfigAnon:            s.cfg.AllowAnonymousUploads,
		ConfigRetention:       formatRetention(s.cfg.AnonymousTTL),
		AnonymousPending:      pending,
		Error:                 r.URL.Query().Get("error"),
		Notice:                r.URL.Query().Get("notice"),
	}
	view.base = s.base(r, "Instance settings")

	s.renderPage(w, http.StatusOK, "admin_settings", view)
}

// handleAdminSaveSettings applies new policy.
func (s *Server) handleAdminSaveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	previous := s.policy()

	window, err := parseRetention(r.FormValue("anonymous_ttl"))
	if err != nil {
		redirectNotice(w, r, "/admin/settings", "error", err.Error())
		return
	}

	next := models.Settings{
		// Absent checkboxes mean off, which is what a form sends.
		AllowSignup:           r.FormValue("allow_signup") == "1",
		AllowAnonymousUploads: r.FormValue("allow_anonymous_uploads") == "1",
		AnonymousTTL:          window,
	}

	if err := s.store.SaveSettings(r.Context(), next); err != nil {
		s.log.Error("admin: save settings", "error", err)
		redirectNotice(w, r, "/admin/settings", "error", "Could not save the settings.")
		return
	}
	if err := s.reloadSettings(r.Context()); err != nil {
		s.log.Error("admin: reload settings", "error", err)
		redirectNotice(w, r, "/admin/settings", "error", "The settings were saved but could not be read back.")
		return
	}

	s.log.Info("instance settings changed",
		"actor", currentUser(r.Context()).ID,
		"allow_signup", next.AllowSignup,
		"allow_anonymous_uploads", next.AllowAnonymousUploads,
		"anonymous_ttl", next.AnonymousTTL.String(),
	)

	notice := "Settings saved."
	if next.AnonymousTTL != previous.AnonymousTTL {
		// Only the retention window reaches backwards: uploads already stored
		// have a deadline fixed at the moment they arrived.
		changed, err := s.store.ApplyAnonymousRetention(r.Context(), next.AnonymousTTL)
		if err != nil {
			s.log.Error("admin: apply retention", "error", err)
			redirectNotice(w, r, "/admin/settings", "error",
				"The settings were saved, but existing uploads could not be updated.")
			return
		}

		s.log.Info("anonymous retention applied to existing uploads",
			"actor", currentUser(r.Context()).ID,
			"window", next.AnonymousTTL.String(),
			"files", changed)

		switch changed {
		case 0:
			notice = "Settings saved. No existing anonymous upload was affected."
		case 1:
			notice = "Settings saved. The deadline on 1 existing anonymous upload was rewritten."
		default:
			notice = fmt.Sprintf("Settings saved. The deadlines on %d existing anonymous uploads were rewritten.", changed)
		}
	}

	redirectNotice(w, r, "/admin/settings", "notice", notice)
}

// handleAdminClearSettings removes the stored overrides, so the configuration
// applies again.
func (s *Server) handleAdminClearSettings(w http.ResponseWriter, r *http.Request) {
	cleared, err := s.store.ClearSettings(r.Context())
	if err != nil {
		s.log.Error("admin: clear settings", "error", err)
		redirectNotice(w, r, "/admin/settings", "error", "Could not clear the settings.")
		return
	}
	if err := s.reloadSettings(r.Context()); err != nil {
		s.log.Error("admin: reload after clear", "error", err)
		redirectNotice(w, r, "/admin/settings", "error", "The settings were cleared but could not be read back.")
		return
	}

	s.log.Info("instance settings cleared", "actor", currentUser(r.Context()).ID, "keys", cleared)
	redirectNotice(w, r, "/admin/settings", "notice",
		"Cleared. The configuration file is in charge again.")
}

// parseRetention reads the retention window from the form.
//
// A bare number means hours, because that is what somebody typing into a
// retention field means by "24". Anything with a unit is parsed as a duration,
// with a trailing "d" for days since Go has no day unit.
func parseRetention(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("The retention window is required.")
	}

	if hours, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if hours <= 0 {
			return 0, fmt.Errorf("The retention window must be positive.")
		}
		return time.Duration(hours) * time.Hour, nil
	}

	if days, err := strconv.ParseInt(strings.TrimSuffix(raw, "d"), 10, 64); err == nil &&
		strings.HasSuffix(raw, "d") {
		if days <= 0 {
			return 0, fmt.Errorf("The retention window must be positive.")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	window, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("Could not read %q as a duration. Try 24, 24h, or 7d.", raw)
	}
	if window <= 0 {
		return 0, fmt.Errorf("The retention window must be positive.")
	}
	return window, nil
}

// formatRetention renders a window the way the form accepts it back: hours
// where that is exact, so a round trip does not turn 24h into 24h0m0s.
func formatRetention(window time.Duration) string {
	if window%time.Hour == 0 {
		return strconv.FormatInt(int64(window/time.Hour), 10) + "h"
	}
	if window%time.Minute == 0 {
		return strconv.FormatInt(int64(window/time.Minute), 10) + "m"
	}
	return window.String()
}

// fileQueryAnonymous selects uploads with no owner. CountFiles ignores the
// limit, and the listing takes the default.
func fileQueryAnonymous() store.FileQuery {
	return store.FileQuery{AnonymousOnly: true}
}
