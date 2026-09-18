// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"imvault/internal/mail"
)

// queueFrom returns the durable queue behind the harness, which tests drive
// directly so the backoff schedule does not have to be waited out.
func queueFrom(t *testing.T, h *harness) *mailQueue {
	t.Helper()

	queue, ok := h.srv.mail.(*mailQueue)
	if !ok {
		t.Fatal("the harness is not running the mail queue")
	}
	return queue
}

func TestMailQueueRetriesUntilDelivered(t *testing.T) {
	h := newQueueHarness(t)
	queue := queueFrom(t, h)

	// A clock the test controls, starting at the moment of the enqueue.
	current := time.Now().UTC()
	queue.now = func() time.Time { return current }

	// The relay is down to begin with.
	h.mailer.failWith = errors.New("relay unreachable")

	alice := h.seedUser("alice")
	if err := h.store.SetEmail(t.Context(), alice.ID, "alice@example.com", false); err != nil {
		t.Fatal(err)
	}

	h.get("/")
	h.postForm("/forgot", url.Values{"csrf_token": {h.csrf()}, "identifier": {"alice"}})

	// The message is queued rather than lost, and one attempt has been made.
	messages, err := h.store.ListMail(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("queued %d messages, want 1", len(messages))
	}
	queued := messages[0]
	if queued.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", queued.Attempts)
	}
	if queued.MailStatus() != "queued" {
		t.Errorf("status = %q, want queued", queued.MailStatus())
	}
	if queued.LastError == "" {
		t.Error("the failure was not recorded")
	}
	if !queued.NextAttemptAt.After(current) {
		t.Error("no retry was scheduled")
	}

	// Sweeping before the backoff elapses does nothing.
	h.srv.drainMail(t.Context())
	if messages, _ = h.store.ListMail(t.Context(), 10); messages[0].Attempts != 1 {
		t.Errorf("the message was retried early (attempts = %d)", messages[0].Attempts)
	}

	// Once the delay passes, the retry happens. The relay is still down, so the
	// attempt count climbs.
	current = current.Add(2 * time.Minute)
	h.srv.drainMail(t.Context())
	messages, _ = h.store.ListMail(t.Context(), 10)
	if messages[0].Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", messages[0].Attempts)
	}

	// The relay comes back.
	h.mailer.failWith = nil
	current = current.Add(time.Hour)
	h.srv.drainMail(t.Context())

	messages, _ = h.store.ListMail(t.Context(), 10)
	if messages[0].MailStatus() != "sent" {
		t.Fatalf("status = %q, want sent", messages[0].MailStatus())
	}

	// And the message was actually handed over, once.
	delivered := h.mailer.sent()
	if len(delivered) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(delivered))
	}
	if delivered[0].To != "alice@example.com" {
		t.Errorf("delivered to %q", delivered[0].To)
	}
	// The reset link in it must still work: the queue preserved the body.
	if token := resetTokenFrom(t, delivered[0]); token == "" {
		t.Error("the delivered message lost its reset link")
	}
}

func TestMailQueueGivesUpAndCanBeRetried(t *testing.T) {
	h := newQueueHarness(t)
	queue := queueFrom(t, h)

	current := time.Now().UTC()
	queue.now = func() time.Time { return current }

	// The harness is configured for three attempts.
	h.mailer.failWith = errors.New("relay unreachable")

	// The administrator has to be the first account, so register before seeding
	// anybody else.
	h.registerForm("boss")

	alice := h.seedUser("alice")
	if err := h.store.SetEmail(t.Context(), alice.ID, "alice@example.com", false); err != nil {
		t.Fatal(err)
	}

	h.get("/")
	h.postForm("/forgot", url.Values{"csrf_token": {h.csrf()}, "identifier": {"alice"}})

	// Drive the schedule until the queue gives up.
	for i := 0; i < 10; i++ {
		messages, err := h.store.ListMail(t.Context(), 10)
		if err != nil {
			t.Fatal(err)
		}
		if messages[0].MailStatus() == "failed" {
			break
		}
		current = current.Add(24 * time.Hour)
		h.srv.drainMail(t.Context())
	}

	messages, err := h.store.ListMail(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if messages[0].MailStatus() != "failed" {
		t.Fatalf("status = %q, want failed after exhausting attempts", messages[0].MailStatus())
	}
	if messages[0].Attempts != h.srv.cfg.MailMaxAttempts {
		t.Errorf("attempts = %d, want %d", messages[0].Attempts, h.srv.cfg.MailMaxAttempts)
	}

	// A failed message is visible to an administrator.
	resp, page := h.get("/admin/mail")
	if resp.StatusCode != 200 {
		t.Fatalf("admin mail page = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(page, "gave up") {
		t.Error("the page does not warn about the failed message")
	}
	if !strings.Contains(page, "relay unreachable") {
		t.Error("the page does not show the reason")
	}

	// The relay recovers, and an administrator requeues it.
	h.mailer.failWith = nil
	resp, _ = h.postForm("/admin/mail/"+itoa64(messages[0].ID)+"/retry", url.Values{
		"csrf_token": {h.csrf()},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("retry = %d, want 303", resp.StatusCode)
	}

	after, err := h.store.ListMail(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if after[0].MailStatus() != "sent" {
		t.Errorf("status after a manual retry = %q, want sent", after[0].MailStatus())
	}
	if len(h.mailer.sent()) != 1 {
		t.Errorf("delivered %d messages, want 1", len(h.mailer.sent()))
	}
}

func TestMailQueueDiscard(t *testing.T) {
	h := newQueueHarness(t)
	queue := queueFrom(t, h)
	queue.now = time.Now

	h.mailer.failWith = errors.New("relay unreachable")

	h.registerForm("boss")

	alice := h.seedUser("alice")
	if err := h.store.SetEmail(t.Context(), alice.ID, "alice@example.com", false); err != nil {
		t.Fatal(err)
	}
	h.get("/")
	h.postForm("/forgot", url.Values{"csrf_token": {h.csrf()}, "identifier": {"alice"}})

	messages, err := h.store.ListMail(t.Context(), 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("expected one queued message, got %d (%v)", len(messages), err)
	}

	resp, _ := h.postForm("/admin/mail/"+itoa64(messages[0].ID)+"/delete", url.Values{
		"csrf_token": {h.csrf()},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("discard = %d, want 303", resp.StatusCode)
	}

	if remaining, _ := h.store.ListMail(t.Context(), 10); len(remaining) != 0 {
		t.Errorf("%d messages survived being discarded", len(remaining))
	}
}

func TestRegistrationQueuesMailWhenTheRelayIsDown(t *testing.T) {
	h := newQueueHarness(t)
	h.mailer.failWith = errors.New("relay unreachable")

	h.get("/")
	resp, _ := h.postForm("/register", url.Values{
		"csrf_token": {h.csrf()},
		"username":   {"alice"},
		"password":   {testPassword},
		"email":      {"alice@example.com"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("register with a broken relay = %d, want 303", resp.StatusCode)
	}

	// The account exists, and the confirmation is waiting rather than lost.
	if _, err := h.store.UserByUsername(t.Context(), "alice"); err != nil {
		t.Fatalf("the account was not created: %v", err)
	}
	messages, err := h.store.ListMail(t.Context(), 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("expected the confirmation to be queued, got %d (%v)", len(messages), err)
	}
	if messages[0].MailStatus() != "queued" {
		t.Errorf("status = %q, want queued", messages[0].MailStatus())
	}
}

func TestMailBackoffGrowsAndIsCapped(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Minute},
		{2, 4 * time.Minute},
		{3, 16 * time.Minute},
		{4, 64 * time.Minute},
		{5, 4*time.Hour + 16*time.Minute}, // 256 minutes, still under the cap
		{6, mailBackoffMax},               // 1024 minutes would exceed it
		{50, mailBackoffMax},
		{0, time.Minute}, // clamps rather than looping forever
	}

	for _, tc := range tests {
		if got := mailBackoff(tc.attempt); got != tc.want {
			t.Errorf("mailBackoff(%d) = %s, want %s", tc.attempt, got, tc.want)
		}
	}
}

func TestMailQueueIsDurableAcrossARestart(t *testing.T) {
	h := newQueueHarness(t)
	queue := queueFrom(t, h)
	queue.now = time.Now

	h.mailer.failWith = errors.New("relay unreachable")

	alice := h.seedUser("alice")
	if err := h.store.SetEmail(t.Context(), alice.ID, "alice@example.com", false); err != nil {
		t.Fatal(err)
	}
	h.get("/")
	h.postForm("/forgot", url.Values{"csrf_token": {h.csrf()}, "identifier": {"alice"}})

	// Nothing is delivered, and nothing is in flight: the queue holds the only
	// copy, in the database.
	if len(h.mailer.sent()) != 0 {
		t.Fatal("the relay was expected to be down")
	}

	// A fresh queue over the same store, as a restarted process would build,
	// finds and delivers the message.
	h.mailer.failWith = nil
	restarted := NewMailQueue(h.store, h.mailer, h.srv.cfg.MailMaxAttempts, time.Minute, h.srv.log)

	// A restarted process starts with a fresh clock, so jump past the scheduled
	// retry to show the message is picked up from the database alone.
	if queue, ok := restarted.(*mailQueue); ok {
		queue.now = func() time.Time { return time.Now().Add(time.Hour) }
	}
	h.srv.mail = restarted
	h.srv.drainMail(t.Context())

	if len(h.mailer.sent()) != 1 {
		t.Errorf("the message did not survive a restart: delivered %d", len(h.mailer.sent()))
	}

	// The original request must not be duplicated by the restart.
	if messages, _ := h.store.ListMail(t.Context(), 10); len(messages) != 1 {
		t.Errorf("%d messages in the queue, want 1", len(messages))
	}

	var _ mail.Sender = restarted
}
