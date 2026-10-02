// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
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

	// A member trusted with invitations is trusted to bring in a few people,
	// not to reopen registration. Their codes admit at most this many accounts
	// and lapse within this many days; only an administrator can mint an
	// unlimited or long-lived one. The store enforces the same bounds; these
	// are checked first so the form can say which bound was crossed.
	memberMaxInviteUses = 25
	memberMaxInviteDays = 30

	// invitesPageSize is how many invitations the list shows per page.
	invitesPageSize = defaultPageSize
)

// inviteRow is one line of the invitation table.
type inviteRow struct {
	Invite  *models.Invite
	Status  string
	Uses    string
	Expiry  string
	Creator string
}

// inviteForm is what the creation form was last filled in with, so a refusal
// can show it again.
type inviteForm struct {
	Label, MaxUses, ExpiresDays string
}

// invitesView backs the invitation page and its panel.
type invitesView struct {
	base
	Rows     []inviteRow
	NewCode  string
	Error    string
	InviteOn bool
	inviteForm
	// InviteURL is the ready-to-send link for a freshly created code.
	InviteURL  string
	InvitePath string
	// Limited is true for a member, whose codes are bounded.
	Limited    bool
	MaxUsesCap int
	MaxDaysCap int
	Pagination paginationView
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

// invitePath is where an account manages invitations: administrators see every
// code under the admin area, members their own under /invites.
func invitePath(user *models.User) string {
	if user.IsAdmin() {
		return "/admin/invites"
	}
	return "/invites"
}

// listInvites reads one page of the invitations an account may see.
func (s *Server) listInvites(r *http.Request, user *models.User) ([]*models.Invite, paginationView, error) {
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * invitesPageSize
	var (
		list  []*models.Invite
		total int
		err   error
	)
	if user.IsAdmin() {
		list, total, err = s.store.ListInvites(r.Context(), invitesPageSize, offset)
	} else {
		list, total, err = s.store.ListInvitesByCreator(r.Context(), user.ID, invitesPageSize, offset)
	}
	if err != nil {
		return nil, paginationView{}, err
	}
	_, pg := pagination(r, total)
	return list, pg, nil
}

// renderInvitesPanel writes the invitation panel, optionally revealing a code
// that was just minted.
func (s *Server) renderInvitesPanel(w http.ResponseWriter, r *http.Request, form inviteForm, newCode, message string) {
	w.Header().Set("Cache-Control", "no-store")
	user := currentUser(r.Context())
	list, pg, err := s.listInvites(r, user)
	if err != nil {
		s.log.Error("invites panel: list", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	view := invitesView{
		base:       s.base(r, "Invitations"),
		Rows:       inviteRows(list),
		NewCode:    newCode,
		Error:      message,
		InviteOn:   s.policy().InviteOnly,
		InvitePath: invitePath(user),
		inviteForm: form,
		Limited:    !user.IsAdmin(),
		MaxUsesCap: maxInviteUses,
		MaxDaysCap: maxInviteLifetimeDays,
		Pagination: pg,
	}
	if view.Limited {
		view.MaxUsesCap, view.MaxDaysCap = memberMaxInviteUses, memberMaxInviteDays
	}
	view.UseAlpine = true
	if newCode != "" {
		view.InviteURL = s.absoluteURL(r, "/register") + "?invite=" + newCode
		view.inviteForm = inviteForm{}
	}
	if view.MaxUses == "" {
		view.MaxUses = "1"
	}
	if view.Limited && view.ExpiresDays == "" {
		view.ExpiresDays = strconv.Itoa(memberMaxInviteDays)
	}
	if isHTMX(r) {
		s.renderPartial(w, "invites_panel", view)
	} else {
		s.renderPage(w, http.StatusOK, "admin_invites", view)
	}
}

// handleInvites shows the invitations an account may manage, and the form to
// create one.
func (s *Server) handleInvites(w http.ResponseWriter, r *http.Request) {
	_, problem := s.flash(r)
	s.renderInvitesPanel(w, r, inviteForm{}, "", problem)
}

// parseInviteForm reads and bounds a creation request. Members get the tighter
// bounds; out-of-range values are refused rather than quietly clamped, so the
// person sees what they are allowed to ask for.
func parseInviteForm(form inviteForm, limited bool) (maxUses int, expiresAt *time.Time, problem string) {
	usesCap, daysCap := maxInviteUses, maxInviteLifetimeDays
	if limited {
		usesCap, daysCap = memberMaxInviteUses, memberMaxInviteDays
	}

	maxUses = 1
	if form.MaxUses != "" {
		parsed, err := strconv.Atoi(form.MaxUses)
		switch {
		case err != nil || parsed < 0:
			return 0, nil, "Uses must be a whole number, or 0 for no limit."
		case limited && (parsed < 1 || parsed > usesCap):
			return 0, nil, fmt.Sprintf("Uses must be between 1 and %d.", usesCap)
		case parsed > usesCap:
			parsed = usesCap
		}
		maxUses = parsed
	}

	days := 0
	if form.ExpiresDays != "" {
		parsed, err := strconv.Atoi(form.ExpiresDays)
		switch {
		case err != nil || parsed < 0:
			return 0, nil, "Expiry must be a whole number of days."
		case limited && (parsed < 1 || parsed > daysCap):
			return 0, nil, fmt.Sprintf("Expiry must be between 1 and %d days.", daysCap)
		case parsed > daysCap:
			parsed = daysCap
		}
		days = parsed
	} else if limited {
		days = daysCap
	}
	if days > 0 {
		e := time.Now().UTC().AddDate(0, 0, days)
		expiresAt = &e
	}
	return maxUses, expiresAt, ""
}

// handleCreateInvite mints a code and reveals it once.
func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	form := inviteForm{
		Label:       strings.TrimSpace(r.PostFormValue("label")),
		MaxUses:     strings.TrimSpace(r.PostFormValue("max_uses")),
		ExpiresDays: strings.TrimSpace(r.PostFormValue("expires_days")),
	}
	maxUses, expiresAt, problem := parseInviteForm(form, !user.IsAdmin())
	if problem != "" {
		s.renderInvitesPanel(w, r, form, "", problem)
		return
	}

	generated := invites.Generate()
	created, err := s.store.CreateInvite(r.Context(), user.ID, models.Truncate(form.Label, 64),
		generated.Prefix, generated.Hash, maxUses, expiresAt)
	if err != nil {
		s.log.Error("create invite", "actor", user.ID, "error", err)
		s.renderInvitesPanel(w, r, form, "", "Could not create the invitation.")
		return
	}

	s.log.Info("invitation created",
		"actor", user.ID,
		"invite", created.ID,
		"prefix", created.Prefix,
		"max_uses", created.MaxUses)

	s.renderInvitesPanel(w, r, inviteForm{}, generated.Full, "")
}

// handleRevokeInvite withdraws a code. Administrators may withdraw any; a
// member only their own.
func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid invitation", http.StatusBadRequest)
		return
	}
	user := currentUser(r.Context())
	if user.IsAdmin() {
		err = s.store.RevokeInvite(r.Context(), id)
	} else {
		err = s.store.RevokeInviteByCreator(r.Context(), id, user.ID)
	}
	if err != nil {
		s.log.Error("invitation revoke", "actor", user.ID, "id", id, "error", err)
		s.renderInvitesPanel(w, r, inviteForm{}, "", "Could not revoke the invitation.")
		return
	}
	s.log.Info("invitation revoked", "actor", user.ID, "invite", id)
	s.renderInvitesPanel(w, r, inviteForm{}, "", "")
}
