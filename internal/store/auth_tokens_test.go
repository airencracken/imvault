// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"

	"imvault/internal/tokens"
)

func TestAuthTokenRoundTrip(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	token, hash := tokens.New()
	expires := time.Now().Add(time.Hour)

	if err := s.CreateAuthToken(ctx, alice.ID, TokenPasswordReset, hash, expires); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Validity is checkable without spending the token.
	for i := 0; i < 2; i++ {
		user, err := s.AuthTokenValid(ctx, hash, TokenPasswordReset, time.Now())
		if err != nil {
			t.Fatalf("valid check %d: %v", i+1, err)
		}
		if user.ID != alice.ID {
			t.Errorf("resolved user %d, want %d", user.ID, alice.ID)
		}
	}

	// Redeeming returns the owner...
	user, err := s.ConsumeAuthToken(ctx, hash, TokenPasswordReset, time.Now())
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if user.ID != alice.ID {
		t.Errorf("consumed for user %d, want %d", user.ID, alice.ID)
	}

	// ...and only once.
	if _, err := s.ConsumeAuthToken(ctx, hash, TokenPasswordReset, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("second consume = %v, want ErrNotFound", err)
	}
	if _, err := s.AuthTokenValid(ctx, hash, TokenPasswordReset, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("valid after consume = %v, want ErrNotFound", err)
	}

	// The caller holds the token, not the hash; be sure they differ so a leak
	// of the stored value is not a leak of the credential.
	if token == hash {
		t.Error("the token and its hash are identical")
	}
}

func TestAuthTokenPurposesAreSeparate(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	_, hash := tokens.New()
	if err := s.CreateAuthToken(ctx, alice.ID, TokenEmailVerify, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AuthTokenValid(ctx, hash, TokenPasswordReset, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("a verification token validated as a reset token: %v", err)
	}
	if _, err := s.AuthTokenValid(ctx, hash, TokenEmailVerify, time.Now()); err != nil {
		t.Errorf("the token failed for its own purpose: %v", err)
	}
}

func TestExpiredAuthTokenIsRejected(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	_, hash := tokens.New()
	if err := s.CreateAuthToken(ctx, alice.ID, TokenPasswordReset, hash, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AuthTokenValid(ctx, hash, TokenPasswordReset, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired token validated: %v", err)
	}
	if _, err := s.ConsumeAuthToken(ctx, hash, TokenPasswordReset, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired token consumed: %v", err)
	}
}

func TestIssuingATokenReplacesEarlierOnes(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	_, first := tokens.New()
	_, second := tokens.New()

	if err := s.CreateAuthToken(ctx, alice.ID, TokenPasswordReset, first, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAuthTokensForUser(ctx, alice.ID, TokenPasswordReset); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAuthToken(ctx, alice.ID, TokenPasswordReset, second, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AuthTokenValid(ctx, first, TokenPasswordReset, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("the superseded token still validates: %v", err)
	}
	if _, err := s.AuthTokenValid(ctx, second, TokenPasswordReset, time.Now()); err != nil {
		t.Errorf("the newest token does not validate: %v", err)
	}
}

func TestDeletingAUserRemovesTheirTokens(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	_, hash := tokens.New()
	if err := s.CreateAuthToken(ctx, alice.ID, TokenPasswordReset, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DB().ExecContext(ctx, `DELETE FROM users WHERE id = ?`, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthTokenValid(ctx, hash, TokenPasswordReset, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("token outlived its owner: %v", err)
	}
}

func TestSetPasswordAndEmail(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	if err := s.SetPassword(ctx, alice.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEmail(ctx, alice.ID, "alice@example.com", false); err != nil {
		t.Fatal(err)
	}

	user := mustLoadUser(t, s, ctx, alice.ID)
	if user.PasswordHash != "new-hash" {
		t.Errorf("password hash = %q", user.PasswordHash)
	}
	if user.Email != "alice@example.com" || user.EmailVerified {
		t.Errorf("email state = %q verified=%v", user.Email, user.EmailVerified)
	}

	// Lookup by address is case-insensitive, matching the uniqueness rule.
	found, err := s.UserByEmail(ctx, "ALICE@EXAMPLE.COM")
	if err != nil {
		t.Fatalf("lookup by email: %v", err)
	}
	if found.ID != alice.ID {
		t.Errorf("resolved %d, want %d", found.ID, alice.ID)
	}

	if _, err := s.UserByEmail(ctx, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown address = %v, want ErrNotFound", err)
	}
	if _, err := s.UserByEmail(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty address = %v, want ErrNotFound", err)
	}
}
