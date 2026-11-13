// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"time"

	"github.com/airencracken/comfylib/analytics"
)

// backgroundTimeout bounds work a request hands off and does not wait for.
const backgroundTimeout = 2 * time.Minute

// inBackground runs work after the response, detached from the request's
// cancellation but still bounded in time.
func (s *Server) inBackground(work func(ctx context.Context)) {
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		ctx, cancel := context.WithTimeout(context.Background(), backgroundTimeout)
		defer cancel()
		work(ctx)
	}()
}

// waitBackground blocks until all handed-off work has finished.
func (s *Server) waitBackground() { s.background.Wait() }

// FlushMetrics writes the request counts still held in memory. Call it on
// shutdown, after the HTTP server has stopped taking requests.
func (s *Server) FlushMetrics(ctx context.Context) error { return s.monitor.Flush(ctx) }

// recordSecurity counts one rejected access attempt for the site health
// dashboard. It names nobody, and a failure to count never changes the answer.
func (s *Server) recordSecurity(ctx context.Context, record func(context.Context, analytics.Executor, time.Time) error) {
	if err := record(context.WithoutCancel(ctx), s.store.AnalyticsDB(), time.Now()); err != nil {
		s.log.Warn("count security event", "error", err)
	}
}
