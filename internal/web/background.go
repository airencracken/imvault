// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"time"
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
