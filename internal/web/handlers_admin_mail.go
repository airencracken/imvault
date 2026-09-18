// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"net/http"
	"strconv"

	"imvault/internal/models"
	"imvault/internal/store"
)

// adminTargetID resolves the {id} path value, writing the error response itself.
func (s *Server) adminTargetID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// handleAdminMail shows the outbound queue.
func (s *Server) handleAdminMail(w http.ResponseWriter, r *http.Request) {
	messages, err := s.store.ListMail(r.Context(), 100)
	if err != nil {
		s.log.Error("admin: list mail", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	pending, failed, err := s.store.MailBacklog(r.Context())
	if err != nil {
		s.log.Error("admin: mail backlog", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	view := adminMailView{
		Rows:        s.adminMailRows(r, messages),
		Pending:     pending,
		Failed:      failed,
		MailEnabled: s.mail.Enabled(),
		Notice:      noticeFromQuery(r),
	}
	view.base = s.base(r, "Outbound mail")
	view.UseAlpine = true

	s.renderPage(w, http.StatusOK, "admin_mail", view)
}

// handleAdminRetryMail puts a failed message back in the queue.
func (s *Server) handleAdminRetryMail(w http.ResponseWriter, r *http.Request) {
	id, ok := s.adminTargetID(w, r)
	if !ok {
		return
	}

	if err := s.store.RetryMail(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.mailRespond(w, r, id, nil, "That message has already been delivered.")
			return
		}
		s.log.Error("admin: retry mail", "id", id, "error", err)
		s.mailRespond(w, r, id, nil, "Could not requeue the message.")
		return
	}

	// Attempt it now rather than waiting for the next sweep.
	s.drainMail(r.Context())

	updated := s.reloadMail(r, id)
	s.mailRespond(w, r, id, updated, "Message requeued.")
}

// handleAdminDeleteMail discards a queued or failed message.
func (s *Server) handleAdminDeleteMail(w http.ResponseWriter, r *http.Request) {
	id, ok := s.adminTargetID(w, r)
	if !ok {
		return
	}

	current := s.reloadMail(r, id)
	if current == nil {
		s.mailRespond(w, r, id, nil, "That message no longer exists.")
		return
	}

	if err := s.store.DeleteMail(r.Context(), id); err != nil {
		s.log.Error("admin: delete mail", "id", id, "error", err)
		s.mailRespond(w, r, id, current, "Could not discard the message.")
		return
	}

	s.mailRespond(w, r, id, nil, "Message discarded.")
}

// mailRespond finishes a queue action: htmx gets the row and a notice, and
// everybody else gets a redirect. A nil message removes the row.
func (s *Server) mailRespond(w http.ResponseWriter, r *http.Request, id int64, message *models.OutboundMail, text string) {
	if !isHTMX(r) {
		redirectNotice(w, r, "/admin/mail", "notice", text)
		return
	}

	var row any
	if message != nil {
		row = s.adminMailRowFor(r, message)
	}
	s.renderAdminRow(w, "admin_mail_row", row, adminNotice{Text: text})
}

// reloadMail fetches one queued message, tolerating its absence.
func (s *Server) reloadMail(r *http.Request, id int64) *models.OutboundMail {
	messages, err := s.store.ListMail(r.Context(), 100)
	if err != nil {
		return nil
	}
	for _, message := range messages {
		if message.ID == id {
			return message
		}
	}
	return nil
}
