// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"imvault/internal/invites"
	"imvault/internal/models"
)

// Bounds on a single invitation, so a typo cannot open the instance wider than
// intended or leave a code alive indefinitely.
const (
	maxInviteUses         = 10000
	maxInviteLifetimeDays = 3650
)

// inviteRow is one line of the invitation table.
type inviteRow struct {
	Invite  *models.Invite
	Status  string
	Uses    string
	Expiry  string
	Creator string
}

// adminInvitesView backs the invitation page.
type adminInvitesView struct {
	base
	Rows     []inviteRow
	NewCode  string
	Error    string
	InviteOn bool
	// InviteURL is the ready-to-send link for a freshly created code.
	InviteURL string
}

func inviteStatus(inv *models.Invite, now time.Time) string {
	switch {
	case inv.Revoked():
		return "revoked"
	case inv.Expired(now):
		return "expired"
	case !inv.Unlimited() && inv.Uses >= inv.MaxUses:
		return "used up"
	default:
		return "open"
	}
}

func inviteRows(list []*models.Invite) []inviteRow {
	now := time.Now()
	rows := make([]inviteRow, 0, len(list))
	for _, inv := range list {
		creator := inv.Creator
		if creator == "" {
			creator = "—"
		}
		expiry := "never"
		if inv.ExpiresAt != nil {
			expiry = models.HumanTime(*inv.ExpiresAt)
		}
		rows = append(rows, inviteRow{
			Invite:  inv,
			Status:  inviteStatus(inv, now),
			Uses:    inv.UsesLabel(),
			Expiry:  expiry,
			Creator: creator,
		})
	}
	return rows
}

// renderInvitesPanel writes the invitation panel, optionally revealing a code
// that was just minted.
func (s *Server) renderInvitesPanel(w http.ResponseWriter, r *http.Request, newCode, message string) {
	list, _, err := s.store.ListInvites(r.Context(), 100, 0)
	if err != nil {
		s.log.Error("invites panel: list", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	view := adminInvitesView{
		base:     s.base(r, "Invitations"),
		Rows:     inviteRows(list),
		NewCode:  newCode,
		Error:    message,
		InviteOn: s.policy().InviteOnly,
	}
	if newCode != "" {
		view.InviteURL = s.absoluteURL(r, "/register") + "?invite=" + newCode
	}

	s.renderPartial(w, "invites_panel", view)
}

// handleAdminInvites shows the invitation list and its controls.
func (s *Server) handleAdminInvites(w http.ResponseWriter, r *http.Request) {
	list, _, err := s.store.ListInvites(r.Context(), 100, 0)
	if err != nil {
		s.log.Error("admin: list invites", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	view := adminInvitesView{
		base:     s.base(r, "Invitations"),
		Rows:     inviteRows(list),
		Error:    strings.TrimSpace(r.URL.Query().Get("error")),
		InviteOn: s.policy().InviteOnly,
	}

	s.renderPage(w, http.StatusOK, "admin_invites", view)
}

// handleAdminCreateInvite mints a code and reveals it once.
func (s *Server) handleAdminCreateInvite(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	label := models.Truncate(strings.TrimSpace(r.FormValue("label")), 64)

	maxUses := 1
	if raw := strings.TrimSpace(r.FormValue("max_uses")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			s.renderInvitesPanel(w, r, "", "Uses must be a whole number, or 0 for no limit.")
			return
		}
		if parsed > maxInviteUses {
			parsed = maxInviteUses
		}
		maxUses = parsed
	}

	var expiresAt *time.Time
	if raw := strings.TrimSpace(r.FormValue("expires_days")); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 0 {
			s.renderInvitesPanel(w, r, "", "Expiry must be a whole number of days.")
			return
		}
		if days > maxInviteLifetimeDays {
			days = maxInviteLifetimeDays
		}
		if days > 0 {
			e := time.Now().UTC().AddDate(0, 0, days)
			expiresAt = &e
		}
	}

	generated := invites.Generate()
	created, err := s.store.CreateInvite(r.Context(), user.ID, label, generated.Prefix, generated.Hash, maxUses, expiresAt)
	if err != nil {
		s.log.Error("admin: create invite", "error", err)
		s.renderInvitesPanel(w, r, "", "Could not create the invitation.")
		return
	}

	s.log.Info("invitation created",
		"actor", user.ID,
		"invite", created.ID,
		"prefix", created.Prefix,
		"max_uses", created.MaxUses)

	s.renderInvitesPanel(w, r, generated.Full, "")
}

// handleAdminRevokeInvite withdraws a code.
func (s *Server) handleAdminRevokeInvite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid invitation", http.StatusBadRequest)
		return
	}

	if err := s.store.RevokeInvite(r.Context(), id); err != nil {
		s.log.Error("admin: revoke invite", "id", id, "error", err)
		s.renderInvitesPanel(w, r, "", "Could not revoke the invitation.")
		return
	}

	s.log.Info("invitation revoked", "actor", currentUser(r.Context()).ID, "invite", id)
	s.renderInvitesPanel(w, r, "", "")
}
