// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"

	"imvault/internal/invites"
	"imvault/internal/models"
)

// newInviteFor mints a single-use invitation and returns its id.
func newInviteFor(t *testing.T, s *Store, ctx context.Context) int64 {
	t.Helper()

	boss, err := s.UserByUsername(ctx, "existing")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserInvitePermission(ctx, boss.ID, true); err != nil {
		t.Fatal(err)
	}
	generated := invites.Generate()
	inv, err := s.CreateInvite(ctx, boss.ID, "test", generated.Prefix, generated.Hash, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	return inv.ID
}

func TestIdentitiesLinkAndUnlink(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")

	link, err := s.LinkIdentity(ctx, alice.ID, "https://id.example", "subject-1", "alice@example.com")
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if link.UserID != alice.ID || link.LastLogin == nil {
		t.Errorf("link = %+v", link)
	}

	// The same subject cannot be claimed twice, whichever account asks.
	if _, err := s.LinkIdentity(ctx, bob.ID, "https://id.example", "subject-1", "alice@example.com"); !errors.Is(err, ErrConflict) {
		t.Errorf("second claim = %v, want ErrConflict", err)
	}

	found, err := s.IdentityBySubject(ctx, "https://id.example", "subject-1")
	if err != nil {
		t.Fatal(err)
	}
	if found.UserID != alice.ID {
		t.Errorf("found account %d, want %d", found.UserID, alice.ID)
	}

	// A different issuer is a different identity, even with the same subject.
	if _, err := s.LinkIdentity(ctx, bob.ID, "https://other.example", "subject-1", "bob@example.com"); err != nil {
		t.Errorf("another issuer was refused: %v", err)
	}

	identities, err := s.IdentitiesByUser(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 {
		t.Fatalf("alice has %d identities, want 1", len(identities))
	}

	// Unlinking requires the owning account, so a guessed id is not enough.
	if err := s.UnlinkIdentity(ctx, link.ID, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("unlink by another account = %v, want ErrNotFound", err)
	}
	if err := s.UnlinkIdentity(ctx, link.ID, alice.ID); err != nil {
		t.Fatalf("unlink by the owner: %v", err)
	}
	if _, err := s.IdentityBySubject(ctx, "https://id.example", "subject-1"); !errors.Is(err, ErrNotFound) {
		t.Error("the identity survived being unlinked")
	}
}

func TestCreateUserWithIdentityIsAtomic(t *testing.T) {
	s, ctx := newTestStore(t)
	mustUser(t, s, ctx, "existing")

	generated := newInviteFor(t, s, ctx)
	inviteID := generated

	// The happy path: the account and the link arrive together.
	user, err := s.CreateUserWithIdentity(ctx, NewUser{Username: "newcomer", PasswordHash: "x"},
		"https://id.example", "subject-1", "new@example.com", &inviteID)
	if err != nil {
		t.Fatalf("create with identity: %v", err)
	}
	if _, err := s.IdentityBySubject(ctx, "https://id.example", "subject-1"); err != nil {
		t.Errorf("the identity was not linked: %v", err)
	}

	// A taken username must leave neither an account nor a link, and must not
	// spend the invitation.
	fresh := newInviteFor(t, s, ctx)
	if _, err := s.CreateUserWithIdentity(ctx, NewUser{Username: "existing", PasswordHash: "x"},
		"https://id.example", "subject-2", "second@example.com", &fresh); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username = %v, want ErrConflict", err)
	}
	if _, err := s.IdentityBySubject(ctx, "https://id.example", "subject-2"); !errors.Is(err, ErrNotFound) {
		t.Error("a refused registration left its identity behind")
	}
	after, err := s.InviteByID(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if after.Uses != 0 {
		t.Errorf("invitation uses = %d after a refused registration, want 0", after.Uses)
	}

	// And the created account is a member, not something more.
	if user.Role != models.RoleMember {
		t.Errorf("role = %q, want member", user.Role)
	}
}
