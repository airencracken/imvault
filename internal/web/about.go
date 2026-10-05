// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"net/http"

	"imvault/internal/models"
)

type siteStaff struct {
	Username string
	Role     models.Role
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB().QueryContext(r.Context(), `SELECT username, role FROM users WHERE disabled = 0 AND role IN ('admin', 'moderator') ORDER BY role, username COLLATE NOCASE`)
	if err != nil {
		s.log.Error("about: staff", "error", err)
		http.Error(w, "Could not read the site information.", 500)
		return
	}
	defer rows.Close()
	staff := []siteStaff{}
	for rows.Next() {
		var person siteStaff
		if err := rows.Scan(&person.Username, &person.Role); err != nil {
			http.Error(w, "Could not read the site information.", 500)
			return
		}
		staff = append(staff, person)
	}
	if rows.Err() != nil {
		http.Error(w, "Could not read the site information.", 500)
		return
	}
	s.renderPage(w, 200, "about", struct {
		base
		HouseRules, OwnerContact string
		Staff                    []siteStaff
	}{s.base(r, "About & house rules"), s.branding().HouseRules, s.branding().OwnerContact, staff})
}
