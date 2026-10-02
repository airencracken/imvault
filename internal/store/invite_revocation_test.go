// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"imvault/internal/invites"
	"imvault/internal/models"
)

func TestDeletingAnIssuerRevokesTheirCodes(t *testing.T) {
	s, ctx, issuer, invitation := invitationFixture(t)
	if err := s.SetUserDisabled(ctx, issuer.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterWithInvite(ctx, NewUser{Username: "while-disabled"}, invitation.ID); !errors.Is(err, ErrInviteUnusable) {
		t.Fatalf("disabled issuer admitted: %v", err)
	}
	if err := s.DeleteUser(ctx, issuer.ID); err != nil {
		t.Fatal(err)
	}
	if user, err := s.RegisterWithInvite(ctx, NewUser{Username: "after-delete"}, invitation.ID); !errors.Is(err, ErrInviteUnusable) {
		t.Fatalf("deleting the issuer brought their code back: %+v, %v", user, err)
	}
	after, err := s.InviteByID(ctx, invitation.ID)
	if err != nil || !after.Revoked() || after.CreatedBy != nil {
		t.Fatalf("the code was not revoked with its issuer: %+v, %v", after, err)
	}
}

func TestDeletingAnAdministratorRevokesTheirCodes(t *testing.T) {
	s, ctx := newTestStore(t)
	admins := make([]*models.User, 2)
	for i, name := range []string{"first", "second"} {
		user, err := s.CreateUser(ctx, NewUser{Username: name, Role: models.RoleAdmin})
		if err != nil {
			t.Fatal(err)
		}
		admins[i] = user
	}
	generated := invites.Generate()
	invitation, err := s.CreateInvite(ctx, admins[1].ID, "family", generated.Prefix, generated.Hash, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, admins[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterWithInvite(ctx, NewUser{Username: "late"}, invitation.ID); !errors.Is(err, ErrInviteUnusable) {
		t.Fatalf("a deleted administrator's code still admits: %v", err)
	}
}

func TestACodeWithNoIssuerIsRefused(t *testing.T) {
	s, ctx, _, invitation := invitationFixture(t)
	// Rows like this predate revocation on deletion.
	if _, err := s.db.ExecContext(ctx, `UPDATE invites SET created_by = NULL WHERE id = ?`, invitation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterWithInvite(ctx, NewUser{Username: "orphan"}, invitation.ID); !errors.Is(err, ErrInviteUnusable) {
		t.Fatalf("an orphaned code admitted somebody: %v", err)
	}
	if _, err := s.CreateUserWithIdentity(ctx, NewUser{Username: "orphan"}, "https://id.example", "s", "", &invitation.ID); !errors.Is(err, ErrInviteUnusable) {
		t.Fatalf("an orphaned code admitted a provider account: %v", err)
	}
}

func TestRevocationLeavesOtherIssuersAndEarlierRevocationsAlone(t *testing.T) {
	s, ctx, issuer, invitation := invitationFixture(t)
	other := mustUser(t, s, ctx, "other")
	if err := s.SetUserInvitePermission(ctx, other.ID, true); err != nil {
		t.Fatal(err)
	}
	generated := invites.Generate()
	theirs, err := s.CreateInvite(ctx, other.ID, "theirs", generated.Prefix, generated.Hash, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	earlier := invites.Generate()
	withdrawn, err := s.CreateInvite(ctx, issuer.ID, "withdrawn", earlier.Prefix, earlier.Hash, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE invites SET revoked_at = 100 WHERE id = ?`, withdrawn.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserInvitePermission(ctx, issuer.ID, false); err != nil {
		t.Fatal(err)
	}
	if after, err := s.InviteByID(ctx, theirs.ID); err != nil || after.Revoked() {
		t.Fatalf("another issuer's code was revoked: %+v, %v", after, err)
	}
	if after, err := s.InviteByID(ctx, withdrawn.ID); err != nil || after.RevokedAt.Unix() != 100 {
		t.Fatalf("an earlier revocation was rewritten: %+v, %v", after, err)
	}
	if after, err := s.InviteByID(ctx, invitation.ID); err != nil || !after.Revoked() {
		t.Fatalf("the issuer's open code survived: %+v, %v", after, err)
	}
}

// failOn installs a trigger that aborts any statement of one kind against a
// table, so a test can make the second half of a transaction fail and check
// that the first half was undone.
func failOn(t *testing.T, s *Store, event, table string) {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(), `CREATE TEMP TRIGGER audit_fail BEFORE `+event+` ON `+table+`
		BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS temp.audit_fail`); err != nil {
			t.Error(err)
		}
	})
}

func TestPermissionRemovalIsAtomicWithRevocation(t *testing.T) {
	s, ctx, issuer, invitation := invitationFixture(t)
	failOn(t, s, "UPDATE", "invites")
	if err := s.SetUserInvitePermission(ctx, issuer.ID, false); err == nil {
		t.Fatal("the injected failure was not reported")
	}
	after, err := s.UserByID(ctx, issuer.ID)
	if err != nil || !after.CanInvite {
		t.Fatalf("permission was removed although its codes were not revoked: %+v, %v", after, err)
	}
	if code, err := s.InviteByID(ctx, invitation.ID); err != nil || code.Revoked() {
		t.Fatalf("half a removal was kept: %+v, %v", code, err)
	}
}

func TestDeletionIsAtomicWithRevocation(t *testing.T) {
	s, ctx, issuer, invitation := invitationFixture(t)
	failOn(t, s, "DELETE", "users")
	if err := s.DeleteUser(ctx, issuer.ID); err == nil {
		t.Fatal("the injected failure was not reported")
	}
	if code, err := s.InviteByID(ctx, invitation.ID); err != nil || code.Revoked() {
		t.Fatalf("codes were revoked by a deletion that did not happen: %+v, %v", code, err)
	}
	if _, err := s.UserByID(ctx, issuer.ID); err != nil {
		t.Fatalf("the account is gone: %v", err)
	}
}

func TestRefusedLastAdministratorDeletionRevokesNothing(t *testing.T) {
	s, ctx := newTestStore(t)
	admin, err := s.CreateUser(ctx, NewUser{Username: "only", Role: models.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	generated := invites.Generate()
	invitation, err := s.CreateInvite(ctx, admin.ID, "", generated.Prefix, generated.Hash, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last administrator deletion: %v", err)
	}
	if code, err := s.InviteByID(ctx, invitation.ID); err != nil || code.Revoked() {
		t.Fatalf("a refused deletion revoked codes: %+v, %v", code, err)
	}
}

func TestDelegatedIssuersAreHeldToTheLimits(t *testing.T) {
	s, ctx, issuer, _ := invitationFixture(t)
	now := time.Now().UTC()
	tooLate := now.Add(MaxDelegatedInviteLifetime + time.Hour)
	inTime := now.Add(MaxDelegatedInviteLifetime - time.Hour)
	refused := []struct {
		uses    int
		expires *time.Time
	}{
		{0, nil}, {-1, nil}, {MaxDelegatedInviteUses + 1, nil}, {1, &tooLate},
	}
	for _, tc := range refused {
		generated := invites.Generate()
		if _, err := s.CreateInvite(ctx, issuer.ID, "", generated.Prefix, generated.Hash, tc.uses, tc.expires); !errors.Is(err, ErrInviteTooBroad) {
			t.Errorf("uses=%d expires=%v: %v", tc.uses, tc.expires, err)
		}
	}
	if _, total, err := s.ListInvitesByCreator(ctx, issuer.ID, 50, 0); err != nil || total != 1 {
		t.Fatalf("refused codes were stored: %d, %v", total, err)
	}

	generated := invites.Generate()
	defaulted, err := s.CreateInvite(ctx, issuer.ID, "", generated.Prefix, generated.Hash, MaxDelegatedInviteUses, nil)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.InviteByID(ctx, defaulted.ID)
	if err != nil || stored.ExpiresAt == nil || stored.ExpiresAt.After(now.Add(MaxDelegatedInviteLifetime+time.Minute)) {
		t.Fatalf("a delegated code with no expiry was not given the ceiling: %+v, %v", stored, err)
	}
	generated = invites.Generate()
	if _, err := s.CreateInvite(ctx, issuer.ID, "", generated.Prefix, generated.Hash, 1, &inTime); err != nil {
		t.Fatalf("a code within the limits was refused: %v", err)
	}

	admin, err := s.CreateUser(ctx, NewUser{Username: "admin", Role: models.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	generated = invites.Generate()
	unlimited, err := s.CreateInvite(ctx, admin.ID, "", generated.Prefix, generated.Hash, 0, nil)
	if err != nil || unlimited.ExpiresAt != nil {
		t.Fatalf("an administrator was held to the delegated limits: %+v, %v", unlimited, err)
	}
}
