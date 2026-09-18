// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"net/http"
	"strconv"

	"imvault/internal/store"
)

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

	s.renderPage(w, http.StatusOK, "admin_mail", adminMailView{
		base:        s.base(r, "Outbound mail"),
		Messages:    messages,
		Pending:     pending,
		Failed:      failed,
		MailEnabled: s.mail.Enabled(),
		Notice:      r.URL.Query().Get("notice"),
		Error:       r.URL.Query().Get("error"),
	})
}

// handleAdminRetryMail puts a failed message back in the queue.
func (s *Server) handleAdminRetryMail(w http.ResponseWriter, r *http.Request) {
	id, ok := s.adminTargetID(w, r)
	if !ok {
		return
	}

	if err := s.store.RetryMail(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			redirectNotice(w, r, "/admin/mail", "error", "That message has already been delivered.")
			return
		}
		s.log.Error("admin: retry mail", "id", id, "error", err)
		redirectNotice(w, r, "/admin/mail", "error", "Could not requeue the message.")
		return
	}

	// Try immediately rather than waiting for the next sweep.
	s.drainMail(r.Context())

	redirectNotice(w, r, "/admin/mail", "notice", "Message requeued.")
}

// handleAdminDeleteMail discards a queued or failed message.
func (s *Server) handleAdminDeleteMail(w http.ResponseWriter, r *http.Request) {
	id, ok := s.adminTargetID(w, r)
	if !ok {
		return
	}

	if err := s.store.DeleteMail(r.Context(), id); err != nil {
		s.log.Error("admin: delete mail", "id", id, "error", err)
		redirectNotice(w, r, "/admin/mail", "error", "Could not discard the message.")
		return
	}

	redirectNotice(w, r, "/admin/mail", "notice", "Message discarded.")
}

// handleAdminRecomputeStorage recalculates every account's usage from the files
// table, repairing any drift left by a crash mid-upload.
func (s *Server) handleAdminRecomputeStorage(w http.ResponseWriter, r *http.Request) {
	corrected, err := s.store.RecomputeStorageUsage(r.Context())
	if err != nil {
		s.log.Error("admin: recompute storage", "error", err)
		redirectNotice(w, r, "/admin", "error", "Could not recalculate storage usage.")
		return
	}

	s.log.Info("admin recalculated storage usage",
		"actor", currentUser(r.Context()).ID, "accounts_corrected", corrected)

	switch corrected {
	case 0:
		redirectNotice(w, r, "/admin", "notice", "Storage usage was already accurate.")
	case 1:
		redirectNotice(w, r, "/admin", "notice", "Corrected the usage recorded for 1 account.")
	default:
		redirectNotice(w, r, "/admin", "notice",
			"Corrected the usage recorded for "+strconv.FormatInt(corrected, 10)+" accounts.")
	}
}

// adminTargetID resolves the {id} path value, writing the error response itself.
func (s *Server) adminTargetID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
