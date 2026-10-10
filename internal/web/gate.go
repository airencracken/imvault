// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"io"
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

// Read deadlines. Without them a client can hold a request open by sending
// its body one byte at a time, and the server has no read timeout of its own
// because a whole-request timeout would also cut off long downloads.
const (
	// formReadTimeout bounds how long an ordinary form body may take to arrive.
	formReadTimeout = time.Minute
	// uploadReadTimeout bounds an upload body, which can be large and can
	// legitimately arrive slowly.
	uploadReadTimeout = 15 * time.Minute
)

// isUploadPath reports whether a request is one of the two upload endpoints.
func isUploadPath(r *http.Request) bool {
	return r.Method == http.MethodPost && (r.URL.Path == "/upload" || r.URL.Path == "/api/v1/upload")
}

// requestLimitsMW bounds every request body before any middleware can read it,
// in size and in time. CSRF tokens may arrive in a form body, so protecting
// only the handlers would leave the earlier CSRF check unbounded.
//
// It deliberately does not take an upload slot. A slot is the scarce thing, and
// handing one out before anything has checked who is asking let an anonymous
// request with no token hold every slot open by trickling its body.
func (s *Server) requestLimitsMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(formRewriteLimit)
		timeout := s.formReadTimeout
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/settings/avatar":
			limit = 3 << 20
		case r.Method == http.MethodPost && r.URL.Path == "/admin/settings/branding-assets":
			limit = 5 << 20
		case isUploadPath(r):
			limit = s.requestSizeLimit(currentUser(r.Context()))
			timeout = s.uploadReadTimeout
		}
		if hasBody(r) {
			s.setReadDeadline(w, r, time.Now().Add(timeout))
			r.Body = &deadlineBody{ReadCloser: http.MaxBytesReader(w, r.Body, limit), clear: func() {
				// Once the body is in, the deadline has done its job. Leaving
				// it would cancel the request when the server starts watching
				// for the client to go away.
				s.setReadDeadline(w, r, time.Time{})
			}}
		}
		defer func() {
			if r.MultipartForm != nil {
				if err := r.MultipartForm.RemoveAll(); err != nil {
					s.log.Warn("remove multipart temporary files", "error", err)
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// hasBody reports whether a request carries a body worth bounding in time.
func hasBody(r *http.Request) bool {
	return r.Body != nil && r.Body != http.NoBody && isMutating(r.Method)
}

// setReadDeadline applies a read deadline where the connection supports one.
func (s *Server) setReadDeadline(w http.ResponseWriter, r *http.Request, deadline time.Time) {
	err := http.NewResponseController(w).SetReadDeadline(deadline)
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.log.Warn("set read deadline", "path", r.URL.Path, "error", err)
	}
}

// deadlineBody clears the read deadline once the body has been read to the
// end.
type deadlineBody struct {
	io.ReadCloser
	clear func()
	done  bool
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) && !b.done {
		b.done = true
		b.clear()
	}
	return n, err
}

// acquireUpload takes a processing slot for an upload that has already passed
// every check that does not cost anything, writing the refusal itself.
func (s *Server) acquireUpload(w http.ResponseWriter, r *http.Request) (func(), bool) {
	release, ok := s.processing.acquire(r.Context())
	if !ok {
		s.uploadBusy(w, r)
		return nil, false
	}
	return release, true
}

func newGate(limit int) *gate {
	if limit < 1 {
		limit = 1
	}
	return &gate{slots: make(chan struct{}, limit), wait: uploadWait}
}

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
	// A real 503 that still carries the message: the client-side handler lets
	// htmx swap this one, because a fragment saying "busy" is more use than a
	// page that silently does nothing.
	s.uploadFailure(w, r, http.StatusServiceUnavailable, message)
}
