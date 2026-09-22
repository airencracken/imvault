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
	values, stored, err := s.store.LoadSettings(ctx, s.configDefaults())
	if err != nil {
		return err
	}

	s.settings.Store(&settingSource{Values: values, Stored: stored})
	return nil
}

// configDefaults is the policy the environment asks for, which applies to
// anything an administrator has not overridden.
func (s *Server) configDefaults() models.Settings {
	return models.Settings{
		AllowSignup:           s.cfg.AllowSignup,
		InviteOnly:            s.cfg.InviteOnly,
		AllowAnonymousUploads: s.cfg.AllowAnonymousUploads,
		AnonymousTTL:          s.cfg.AnonymousTTL,
		DefaultVisibility:     s.cfg.DefaultVisibility,
		MaxTotalBytes:         s.cfg.MaxTotalBytes,
	}
}

// policy is the current instance policy.
func (s *Server) policy() models.Settings {
	if current := s.settings.Load(); current != nil {
		return current.Values
	}
	// Only reachable if the server was constructed without loading, which
	// should not happen; the configuration is the safe answer.
	return s.configDefaults()
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
	InviteOnly            bool
	AllowAnonymousUploads bool
	// Retention is the window as written for a person, for example "24h" or
	// "7d0h".
	Retention string
	// MaxTotalMB is the instance-wide ceiling, in MiB, for the form. Zero means
	// unlimited.
	MaxTotalMB int64
	// Visibility is the level a new upload gets.
	Visibility models.Visibility
	// Levels is every level, in the order they are offered.
	Levels []models.Visibility
	// Profiles are the named bundles, so the three ways of running this do not
	// have to be reconstructed setting by setting.
	Profiles []instanceProfile
	// Stored marks which settings are set here rather than in the environment.
	Stored map[string]bool
	// Config values, shown so an operator can see what clearing would restore.
	ConfigSignup     bool
	ConfigInviteOnly bool
	ConfigAnon       bool
	ConfigRetention  string
	ConfigVisibility models.Visibility
	ConfigMaxTotalMB int64
	AnonymousPending int
	Error            string
	Notice           string
}

// instanceProfile is a named bundle of instance policy.
//
// Personal, group, and public hosts are the same program at different points
// along a few axes rather than three programs, so the profiles are a starting
// point rather than a mode. Applying one leaves the retention window alone on
// purpose: that setting reaches backwards over uploads already stored, and a
// button labelled "Public" should not quietly purge somebody's files.
type instanceProfile struct {
	Key        string
	Name       string
	Summary    string
	Signup     bool
	InviteOnly bool
	Anonymous  bool
	Visibility models.Visibility
}

func instanceProfiles() []instanceProfile {
	return []instanceProfile{
		{
			Key:        "personal",
			Name:       "Personal",
			Summary:    "One person. Nobody joins, nothing is shared, and uploads are yours alone.",
			Signup:     false,
			Anonymous:  false,
			Visibility: models.VisibilityPrivate,
		},
		{
			Key:        "group",
			Name:       "Group",
			Summary:    "A community. Accounts join by invitation, uploads are visible to members, and anonymous uploads are off.",
			Signup:     true,
			InviteOnly: true,
			Anonymous:  false,
			Visibility: models.VisibilityMembers,
		},
		{
			Key:        "public",
			Name:       "Public",
			Summary:    "Open to the world. Anyone may register or upload anonymously, and uploads are public.",
			Signup:     true,
			Anonymous:  true,
			Visibility: models.VisibilityPublic,
		},
	}
}

func profileFor(key string) (instanceProfile, bool) {
	for _, profile := range instanceProfiles() {
		if profile.Key == key {
			return profile, true
		}
	}
	return instanceProfile{}, false
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
		InviteOnly:            policy.InviteOnly,
		AllowAnonymousUploads: policy.AllowAnonymousUploads,
		Retention:             formatRetention(policy.AnonymousTTL),
		MaxTotalMB:            policy.MaxTotalBytes >> 20,
		Visibility:            policy.DefaultVisibility,
		Levels:                models.VisibilityLevels(),
		Profiles:              instanceProfiles(),
		Stored:                s.settingSources(),
		ConfigSignup:          s.cfg.AllowSignup,
		ConfigInviteOnly:      s.cfg.InviteOnly,
		ConfigAnon:            s.cfg.AllowAnonymousUploads,
		ConfigRetention:       formatRetention(s.cfg.AnonymousTTL),
		ConfigVisibility:      s.cfg.DefaultVisibility,
		ConfigMaxTotalMB:      s.cfg.MaxTotalBytes >> 20,
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

	maxTotal, err := parseMegabytes(r.FormValue("max_total_mb"))
	if err != nil {
		redirectNotice(w, r, "/admin/settings", "error", err.Error())
		return
	}

	next := models.Settings{
		// Absent checkboxes mean off, which is what a form sends.
		AllowSignup:           r.FormValue("allow_signup") == "1",
		InviteOnly:            r.FormValue("invite_only") == "1",
		AllowAnonymousUploads: r.FormValue("allow_anonymous_uploads") == "1",
		AnonymousTTL:          previous.AnonymousTTL,
		DefaultVisibility:     models.ParseVisibility(r.FormValue("default_visibility")),
		MaxTotalBytes:         maxTotal,
	}

	applied := ""
	if key := strings.TrimSpace(r.FormValue("profile")); key != "" {
		profile, ok := profileFor(key)
		if !ok {
			redirectNotice(w, r, "/admin/settings", "error", "No such profile.")
			return
		}
		// A profile sets the policy axes and leaves the retention window as it
		// is, because changing that rewrites the deadline on uploads already
		// stored.
		next.AllowSignup = profile.Signup
		next.InviteOnly = profile.InviteOnly
		next.AllowAnonymousUploads = profile.Anonymous
		next.DefaultVisibility = profile.Visibility
		next.MaxTotalBytes = previous.MaxTotalBytes
		applied = profile.Name
	} else {
		window, err := parseRetention(r.FormValue("anonymous_ttl"))
		if err != nil {
			redirectNotice(w, r, "/admin/settings", "error", err.Error())
			return
		}
		next.AnonymousTTL = window
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
		"profile", applied,
		"allow_signup", next.AllowSignup,
		"invite_only", next.InviteOnly,
		"allow_anonymous_uploads", next.AllowAnonymousUploads,
		"anonymous_ttl", next.AnonymousTTL.String(),
		"default_visibility", string(next.DefaultVisibility),
		"max_total_bytes", next.MaxTotalBytes,
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

// parseMegabytes reads a size in MiB, where zero means no ceiling. A profile
// leaves it alone for the same reason it leaves the retention window alone:
// it is an operator's decision about this particular box, not a shape of
// instance.
func parseMegabytes(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}

	megabytes, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || megabytes < 0 {
		return 0, fmt.Errorf("The storage ceiling must be a whole number of MiB, or blank for no ceiling.")
	}
	return megabytes << 20, nil
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
