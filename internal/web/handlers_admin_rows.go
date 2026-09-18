// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"net/http"
	"strings"

	"imvault/internal/models"
)

// Admin actions answer an htmx request with the affected table row plus, out of
// band, a notice placed elsewhere on the page. A single response can therefore
// update the row and report what happened without a reload.
//
// Requests without htmx still get a redirect, so the admin area keeps working
// with JavaScript disabled.

// adminUserRowFor builds the row for one account, in the shape the partial
// expects.
func (s *Server) adminUserRowFor(r *http.Request, user *models.User, errMsg string) adminUserRow {
	viewer := currentUser(r.Context())

	return adminUserRow{
		User:      user,
		CSRFToken: csrfToken(r.Context()),
		Search:    strings.ToLower(user.Username + " " + user.Email),
		Self:      viewer != nil && viewer.ID == user.ID,
		Error:     errMsg,
		Roles:     models.RoleLevels(),
	}
}

// adminMailRowFor builds the row for one queued message.
func (s *Server) adminMailRowFor(r *http.Request, message *models.OutboundMail) adminMailRow {
	return adminMailRow{
		Message:   message,
		CSRFToken: csrfToken(r.Context()),
		Search:    strings.ToLower(message.Recipient + " " + message.Subject + " " + message.MailStatus()),
	}
}

// adminUserRows loads the account list in the shape the table expects.
func (s *Server) adminUserRows(r *http.Request, users []*models.User) []adminUserRow {
	rows := make([]adminUserRow, 0, len(users))
	for _, user := range users {
		rows = append(rows, s.adminUserRowFor(r, user, ""))
	}
	return rows
}

// adminMailRows loads the queue in the shape the table expects.
func (s *Server) adminMailRows(r *http.Request, messages []*models.OutboundMail) []adminMailRow {
	rows := make([]adminMailRow, 0, len(messages))
	for _, message := range messages {
		rows = append(rows, s.adminMailRowFor(r, message))
	}
	return rows
}

// noticeFromQuery rebuilds the notice a redirect carried, so the non-JavaScript
// path shows the same message the htmx path would have swapped in.
func noticeFromQuery(r *http.Request) adminNotice {
	if err := strings.TrimSpace(r.URL.Query().Get("error")); err != "" {
		return adminNotice{Text: err, Error: true}
	}
	return adminNotice{Text: strings.TrimSpace(r.URL.Query().Get("notice"))}
}

// renderAdminUsersPage renders the account table in full.
//
// It is the fallback for an action that has something to show — issuing a reset
// link produces a value that exists nowhere else — and a redirect would drop
// it. With htmx the same information rides along in a fragment instead.
func (s *Server) renderAdminUsersPage(w http.ResponseWriter, r *http.Request, status int, notice adminNotice) {
	users, total, err := s.store.ListUsers(r.Context(), adminPageSize, 0)
	if err != nil {
		s.log.Error("admin: list users", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	_, pg := pagination(r, total)

	view := adminUsersView{
		Rows:       s.adminUserRows(r, users),
		Pagination: pg,
		Notice:     notice,
	}
	view.base = s.base(r, "Users")
	view.UseAlpine = true

	s.renderPage(w, status, "admin_users", view)
}

// renderAdminRow writes a row fragment followed by an out-of-band notice.
//
// A nil row means "this row is gone": the empty body removes it, because the
// caller targets the row with an outerHTML swap.
func (s *Server) renderAdminRow(w http.ResponseWriter, rowTemplate string, row any, notice adminNotice) {
	var buf bytes.Buffer

	if row != nil {
		if err := s.render.partial(&buf, rowTemplate, row); err != nil {
			s.log.Error("render admin row", "template", rowTemplate, "error", err)
			http.Error(w, "template error", http.StatusInternalServerError)
			return
		}
	}

	if notice.Text != "" {
		if err := s.render.partial(&buf, "admin_notice", notice); err != nil {
			s.log.Error("render admin notice", "error", err)
			http.Error(w, "template error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

// adminRespond finishes an admin action: htmx gets the row and a notice, and
// everybody else gets the redirect the pages have always used.
//
// A nil user means the row no longer exists.
func (s *Server) adminRespond(w http.ResponseWriter, r *http.Request, user *models.User, notice adminNotice, errMsg, redirectPath string) {
	if !isHTMX(r) {
		key := "notice"
		if errMsg != "" {
			key = "error"
		}
		message := notice.Text
		if errMsg != "" {
			message = errMsg
		}
		redirectNotice(w, r, redirectPath, key, message)
		return
	}

	if errMsg != "" {
		notice = adminNotice{Text: errMsg, Error: true}
	}

	var row any
	if user != nil {
		row = s.adminUserRowFor(r, user, errMsg)
	}
	s.renderAdminRow(w, "admin_user_row", row, notice)
}

// reloadAdminUser fetches an account for the row response, tolerating its
// absence.
func (s *Server) reloadAdminUser(r *http.Request, id int64) *models.User {
	user, err := s.store.UserByID(r.Context(), id)
	if err != nil {
		return nil
	}
	return user
}
