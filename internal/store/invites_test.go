// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"

	"imvault/internal/invites"
	"imvault/internal/models"
)

func TestRegisterWithInviteIsAtomic(t *testing.T) {
	s, ctx := newTestStore(t)
	boss := mustUser(t, s, ctx, "boss")
	if err := s.SetUserInvitePermission(ctx, boss.ID, true); err != nil {
		t.Fatal(err)
	}

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
	if err := s.SetUserInvitePermission(ctx, boss.ID, true); err != nil {
		t.Fatal(err)
	}

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

func TestDelegatedInvitePermissionAndAttribution(t *testing.T) {
	s, ctx := newTestStore(t)
	admin, err := s.CreateUser(ctx, NewUser{Username: "admin", Role: models.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	delegate, err := s.CreateUser(ctx, NewUser{Username: "delegate"})
	if err != nil {
		t.Fatal(err)
	}
	generated := invites.Generate()
	if _, err := s.CreateInvite(ctx, delegate.ID, "denied", generated.Prefix, generated.Hash, 1, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ungranted member created invite: %v", err)
	}
	if err := s.SetUserInvitePermission(ctx, delegate.ID, true); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateInvite(ctx, delegate.ID, "family", generated.Prefix, generated.Hash, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	newcomer, err := s.RegisterWithInvite(ctx, NewUser{Username: "newcomer", PasswordHash: "hash"}, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if newcomer.InvitedBy == nil || *newcomer.InvitedBy != delegate.ID || newcomer.InvitedByUsername != delegate.Username || newcomer.InvitationID == nil || *newcomer.InvitationID != created.ID {
		t.Fatalf("registration lost its invitation lineage: %+v", newcomer)
	}
	all, total, err := s.ListInvites(ctx, 10, 0)
	if err != nil || total != 1 || len(all) != 1 {
		t.Fatalf("all invites: %+v total=%d err=%v", all, total, err)
	}
	delegated, total, err := s.ListInvitesByCreator(ctx, delegate.ID, 10, 0)
	if err != nil || total != 1 || len(delegated) != 1 || delegated[0].Creator != delegate.Username {
		t.Fatalf("delegate invites: %+v total=%d err=%v", delegated, total, err)
	}
	if err := s.RevokeInviteByCreator(ctx, created.ID, admin.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another account revoked delegated invite: %v", err)
	}
}
