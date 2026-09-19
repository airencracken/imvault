// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// gate bounds how many uploads are being processed at once.
//
// Rate limiting and this are different controls, and an instance needs both. A
// token bucket bounds how many uploads one identity may start per hour, but a
// burst allowance still lets it start several at the same instant, and every
// identity gets its own bucket. What nothing bounded before was the total: a
// video upload shells out to ffmpeg inside the request, so the sum of
// concurrent invocations was (identities × burst) with no ceiling. On a small
// host that is a one-person outage.
//
// So this is a semaphore, not a counter: at most limit uploads exist at a time,
// across everybody.
type gate struct {
	slots chan struct{}
	// wait is how long a request waits for a slot before giving up. Failing
	// immediately would be simpler, but a person dragging photos into a page
	// deserves a short queue rather than a retry they have to perform
	// themselves.
	wait time.Duration
}

// uploadWait is how long an upload waits for a slot. It is deliberately short:
// past a few seconds the honest answer is "come back in a moment", and holding
// the connection open is itself a resource.
const uploadWait = 10 * time.Second

func newGate(limit int) *gate {
	if limit < 1 {
		limit = 1
	}
	return &gate{slots: make(chan struct{}, limit), wait: uploadWait}
}

// limit is how many uploads may run at once, which the busy message reports.
func (g *gate) limit() int { return cap(g.slots) }

// acquire takes a slot, reporting false if none came free in time. The returned
// function releases it and must be called exactly once.
func (g *gate) acquire(ctx context.Context) (func(), bool) {
	// Fast path: an idle instance should not pay for a timer.
	select {
	case g.slots <- struct{}{}:
		return g.release, true
	default:
	}

	timer := time.NewTimer(g.wait)
	defer timer.Stop()

	select {
	case g.slots <- struct{}{}:
		return g.release, true
	case <-timer.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

func (g *gate) release() { <-g.slots }

// uploadBusy reports that every slot is taken.
//
// A 503 with Retry-After is the honest answer: the work was not attempted, and
// the client should try again rather than assume it failed. htmx gets the same
// shape of fragment a failed upload gets, so the uploader page reports it in
// place instead of replacing the page with a status code.
func (s *Server) uploadBusy(w http.ResponseWriter, r *http.Request) {
	message := "The server is busy with other uploads. Try again in a moment."

	w.Header().Set("Retry-After", strconv.Itoa(int(uploadWait.Seconds())))

	if isAPIPath(r) {
		writeAPIError(w, http.StatusServiceUnavailable, message)
		return
	}
	if isHTMX(r) {
		// A real 503 that still carries the message: the client-side handler
		// lets htmx swap this one, because a fragment saying "busy" is more use
		// than a page that silently does nothing.
		s.renderPartialStatus(w, http.StatusServiceUnavailable, "upload_result", uploadResultView{
			base:   s.base(r, "Upload"),
			Grid:   s.grid(r, nil, false, false, "", ""),
			Errors: []string{message},
		})
		return
	}

	http.Error(w, message, http.StatusServiceUnavailable)
}
