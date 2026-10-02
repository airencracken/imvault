// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"
	"time"
)

func TestDeliveredMailForgetsItsBody(t *testing.T) {
	s, ctx := newTestStore(t)
	msg, err := s.EnqueueMail(ctx, "a@example.com", "Reset", "https://img.example.com/reset?token=live-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMailSent(ctx, msg.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListMail(ctx, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v, %v", list, err)
	}
	if list[0].Body != "" || list[0].SentAt == nil {
		t.Fatalf("a delivered message kept its link: %+v", list[0])
	}
}

func TestPruningDropsOldDeliveredAndAbandonedMailOnly(t *testing.T) {
	s, ctx := newTestStore(t)
	now := time.Now()
	old, recent := now.Add(-10*24*time.Hour), now.Add(-time.Hour)
	enqueue := func(subject string) int64 {
		t.Helper()
		msg, err := s.EnqueueMail(ctx, "a@example.com", subject, "body")
		if err != nil {
			t.Fatal(err)
		}
		return msg.ID
	}
	if err := s.MarkMailSent(ctx, enqueue("old sent"), old); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMailSent(ctx, enqueue("recent sent"), recent); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMailAttempt(ctx, enqueue("old failed"), 5, old, "relay down", &old); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMailAttempt(ctx, enqueue("recent failed"), 5, recent, "relay down", &recent); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMailAttempt(ctx, enqueue("old pending"), 1, old, "relay down", nil); err != nil {
		t.Fatal(err)
	}
	pruned, err := s.PruneSentMail(ctx, now.Add(-7*24*time.Hour))
	if err != nil || pruned != 2 {
		t.Fatalf("pruned %d (%v), want the two old finished messages", pruned, err)
	}
	list, err := s.ListMail(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{}
	for _, m := range list {
		left[m.Subject] = true
	}
	for _, subject := range []string{"recent sent", "recent failed", "old pending"} {
		if !left[subject] {
			t.Errorf("%q was pruned", subject)
		}
	}
}
