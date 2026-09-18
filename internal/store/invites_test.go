// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"

	"imvault/internal/invites"
)

func TestRegisterWithInviteIsAtomic(t *testing.T) {
	s, ctx := newTestStore(t)
	boss := mustUser(t, s, ctx, "boss")

	mint := func(maxUses int) int64 {
		t.Helper()
		generated := invites.Generate()
		inv, err := s.CreateInvite(ctx, boss.ID, "test", generated.Prefix, generated.Hash, maxUses, nil)
		if err != nil {
			t.Fatalf("create invite: %v", err)
		}
		return inv.ID
	}

	// The happy path.
	one := mint(1)
	if _, err := s.RegisterWithInvite(ctx, NewUser{Username: "first", PasswordHash: "x"}, one); err != nil {
		t.Fatalf("register: %v", err)
	}

	// A single-use code cannot be spent twice, and the refusal leaves nothing
	// behind.
	if _, err := s.RegisterWithInvite(ctx, NewUser{Username: "second", PasswordHash: "x"}, one); !errors.Is(err, ErrInviteUnusable) {
		t.Errorf("second use = %v, want ErrInviteUnusable", err)
	}
	if _, err := s.UserByUsername(ctx, "second"); !errors.Is(err, ErrNotFound) {
		t.Error("an account was created by a refused redemption")
	}

	// A registration that fails for a taken name must roll the use back,
	// otherwise a mistyped username costs somebody their invitation.
	fresh := mint(1)
	if _, err := s.RegisterWithInvite(ctx, NewUser{Username: "boss", PasswordHash: "x"}, fresh); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate username = %v, want ErrConflict", err)
	}
	after, err := s.InviteByID(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if after.Uses != 0 {
		t.Errorf("uses = %d after a refused registration, want 0", after.Uses)
	}
	if !after.Usable(time.Now()) {
		t.Error("the invitation was spent without an account to show for it")
	}
}

func TestRevokeInviteIsIdempotent(t *testing.T) {
	s, ctx := newTestStore(t)
	boss := mustUser(t, s, ctx, "boss")

	generated := invites.Generate()
	inv, err := s.CreateInvite(ctx, boss.ID, "test", generated.Prefix, generated.Hash, 1, nil)
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := s.RevokeInvite(ctx, inv.ID); err != nil {
			t.Fatalf("revoke attempt %d: %v", attempt, err)
		}
	}

	after, err := s.InviteByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Revoked() {
		t.Error("the invitation is not revoked")
	}
	if after.Usable(time.Now()) {
		t.Error("a revoked invitation is still usable")
	}
}
