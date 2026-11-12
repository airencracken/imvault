// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"github.com/airencracken/comfylib/admin"
	"net/http"
	"time"
)

type metricsView struct {
	base
	Overview admin.View
}

func (s *Server) handleAdminMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	metrics, err := s.store.Metrics(r.Context(), time.Now())
	if err != nil {
		s.log.Error("admin metrics", "error", err)
		http.Error(w, "Could not load instance metrics", 500)
		return
	}
	overview := admin.WithSuite(admin.Runtime(s.cfg.Version, s.Backups.Status()), "IMVAULT_")
	overview.Metrics = metrics
	s.renderPage(w, 200, "metrics", metricsView{base: s.base(r, "Instance metrics"), Overview: overview})
}
