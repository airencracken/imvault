// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
)

func TestProviderAccountsStartWithoutAPassword(t *testing.T) {
	s, ctx := newTestStore(t)
	local := mustUser(t, s, ctx, "local")

	if set, err := s.PasswordSet(ctx, local.ID); err != nil || !set {
		t.Fatalf("an ordinary account: set=%v err=%v, want a password", set, err)
	}

	user, err := s.CreateUserWithIdentity(ctx, NewUser{Username: "viaprovider", PasswordHash: "random"},
		"https://id.example", "subject-1", "p@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if set, err := s.PasswordSet(ctx, user.ID); err != nil || set {
		t.Fatalf("a provider account: set=%v err=%v, want no password", set, err)
	}

	// Choosing a password, by whatever path, records it.
	if err := s.SetPassword(ctx, user.ID, "chosen"); err != nil {
		t.Fatal(err)
	}
	if set, err := s.PasswordSet(ctx, user.ID); err != nil || !set {
		t.Errorf("after choosing one: set=%v err=%v, want a password", set, err)
	}

	if _, err := s.PasswordSet(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing account = %v, want ErrNotFound", err)
	}
}

// The account, its identity, the invitation use and the password flag arrive
// together or not at all.
func TestProviderAccountPasswordFlagIsAtomic(t *testing.T) {
	s, ctx := newTestStore(t)
	mustUser(t, s, ctx, "existing")

	fresh := newInviteFor(t, s, ctx)
	if _, err := s.CreateUserWithIdentity(ctx, NewUser{Username: "existing", PasswordHash: "x"},
		"https://id.example", "subject-2", "second@example.com", &fresh); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username = %v, want ErrConflict", err)
	}
	if _, err := s.IdentityBySubject(ctx, "https://id.example", "subject-2"); !errors.Is(err, ErrNotFound) {
		t.Error("a refused registration left its identity behind")
	}
	if after, err := s.InviteByID(ctx, fresh); err != nil || after.Uses != 0 {
		t.Errorf("invitation after a refused registration: %+v %v", after, err)
	}

	// A subject already claimed rolls the whole account back too.
	taken := mustUser(t, s, ctx, "taken")
	if _, err := s.LinkIdentity(ctx, taken.ID, "https://id.example", "subject-3", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUserWithIdentity(ctx, NewUser{Username: "newcomer", PasswordHash: "x"},
		"https://id.example", "subject-3", "", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("claimed subject = %v, want ErrConflict", err)
	}
	if _, err := s.UserByUsername(ctx, "newcomer"); !errors.Is(err, ErrNotFound) {
		t.Error("a refused link left its account behind")
	}
}

// An invitation used through a provider registration is spent and attributed
// exactly as one used through the password form.
func TestProviderAccountsRecordTheirInvitation(t *testing.T) {
	s, ctx := newTestStore(t)
	issuer := mustUser(t, s, ctx, "existing")
	inviteID := newInviteFor(t, s, ctx)

	user, err := s.CreateUserWithIdentity(ctx, NewUser{Username: "invited", PasswordHash: "x"},
		"https://id.example", "subject-9", "", &inviteID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.UserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.InvitedBy == nil || *stored.InvitedBy != issuer.ID || stored.InvitedByUsername != "existing" {
		t.Errorf("attribution = %v %q, want account %d", stored.InvitedBy, stored.InvitedByUsername, issuer.ID)
	}
	if inv, err := s.InviteByID(ctx, inviteID); err != nil || inv.Uses != 1 {
		t.Errorf("invitation after use: %+v %v", inv, err)
	}
}
