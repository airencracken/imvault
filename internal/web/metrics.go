// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"github.com/airencracken/comfylib/admin"
	"github.com/airencracken/comfylib/analytics"
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
		s.fail(w, r, 500, "Site figures couldn't be loaded. Please try again.")
		return
	}
	activity, err := analytics.Collect(r.Context(), s.store.AnalyticsDB(), "", time.Now())
	if err != nil {
		s.log.Error("admin site health", "error", err)
		s.fail(w, r, 500, "Site health figures couldn't be loaded. Please try again.")
		return
	}
	overview := admin.WithSuite(admin.Runtime(s.cfg.Version, s.Backups.Status()), "IMVAULT_")
	overview.Timezone = preferredTimezone(r)
	overview.Metrics = metrics
	overview.Activity = &activity
	overview.BackURL = "/admin"
	s.renderPage(w, r, 200, "metrics", metricsView{base: s.base(r, "Instance metrics"), Overview: overview})
}
