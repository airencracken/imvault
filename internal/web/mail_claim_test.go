// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"sync"
	"testing"
	"time"

	"imvault/internal/mail"
)

// A message being delivered by the request that queued it is not picked up a
// second time by the sweep while the relay is still answering.
func TestASlowDeliveryIsNotSentTwice(t *testing.T) {
	h := newQueueHarness(t)
	h.mailer.gate = make(chan struct{})
	h.mailer.entered = make(chan struct{}, 4)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := h.srv.mail.Send(t.Context(), mail.Message{To: "a@example.com", Subject: "s", Body: "b"}); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-h.mailer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first delivery never reached the relay")
	}

	// The sweep runs while the relay is still busy with the first attempt.
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.srv.drainMail(t.Context())
	}()
	time.Sleep(200 * time.Millisecond)
	close(h.mailer.gate)
	wg.Wait()

	if sent := len(h.mailer.sent()); sent != 1 {
		t.Fatalf("the message was delivered %d times, want once", sent)
	}
}
