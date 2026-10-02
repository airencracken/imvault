// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"sync"
	"testing"
	"time"
)

// Of any number of callers racing for one due message, exactly one wins it.
func TestClaimMailIsExclusive(t *testing.T) {
	s, ctx := newTestStore(t)
	record, err := s.EnqueueMail(ctx, "a@example.com", "subject", "body")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			won, err := s.ClaimMail(ctx, record.ID, now, now.Add(time.Minute))
			if err != nil {
				t.Error(err)
				return
			}
			if won {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d callers claimed one message, want 1", wins)
	}

	// While claimed it is not due, so the sweep does not see it.
	due, err := s.DueMail(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Error("a claimed message is still listed as due")
	}

	// Once the claim lapses, it can be claimed again.
	if won, err := s.ClaimMail(ctx, record.ID, now.Add(2*time.Minute), now.Add(3*time.Minute)); err != nil || !won {
		t.Errorf("a lapsed claim could not be renewed: %v %v", won, err)
	}

	// A delivered message cannot be claimed at all.
	if err := s.MarkMailSent(ctx, record.ID, now); err != nil {
		t.Fatal(err)
	}
	if won, err := s.ClaimMail(ctx, record.ID, now.Add(time.Hour), now.Add(2*time.Hour)); err != nil || won {
		t.Errorf("a sent message was claimed: %v %v", won, err)
	}
}
