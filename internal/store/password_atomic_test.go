package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestPasswordMutationRollbackPreservesCredentials(t *testing.T) {
	for _, table := range []string{"users", "sessions", "auth_tokens"} {
		for _, reset := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reset=%t", table, reset), func(t *testing.T) {
				s, ctx := newTestStore(t)
				u := mustUser(t, s, ctx, "alice")
				if err := s.CreateSession(ctx, "old-session", u.ID, time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				if err := s.CreateAuthToken(ctx, u.ID, TokenPasswordReset, "reset-hash", time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				event := "DELETE"
				if table == "users" {
					event = "UPDATE"
				}
				if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER audit_fail BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, 'injected failure'); END`, event, table)); err != nil {
					t.Fatal(err)
				}
				var err error
				if reset {
					_, err = s.ResetPassword(ctx, "reset-hash", "replacement-hash", time.Now())
				} else {
					err = s.SetPassword(ctx, u.ID, "replacement-hash")
				}
				if err == nil {
					t.Fatal("injected write failure succeeded")
				}
				current, err := s.UserByID(ctx, u.ID)
				if err != nil || current.PasswordHash != u.PasswordHash {
					t.Fatal("failed mutation changed the password")
				}
				if _, err := s.UserBySession(ctx, "old-session", time.Now()); err != nil {
					t.Fatalf("failed mutation removed old session: %v", err)
				}
				if _, err := s.AuthTokenValid(ctx, "reset-hash", TokenPasswordReset, time.Now()); err != nil {
					t.Fatalf("failed mutation spent reset link: %v", err)
				}
			})
		}
	}
}

func TestConcurrentResetRedemptionHasExactlyOneWinner(t *testing.T) {
	s, ctx := newTestStore(t)
	u := mustUser(t, s, ctx, "alice")
	if err := s.CreateAuthToken(ctx, u.ID, TokenPasswordReset, "reset-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := range 12 {
		wg.Go(func() {
			_, err := s.ResetPassword(ctx, "reset-hash", fmt.Sprintf("hash-%d", i), time.Now())
			results <- err
		})
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("reset winners = %d, want one", winners)
	}
}
