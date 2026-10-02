// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"imvault/internal/invites"
	"imvault/internal/models"
)

func invitationFixture(t *testing.T) (*Store, context.Context, *models.User, *models.Invite) {
	t.Helper()
	s, ctx := newTestStore(t)
	issuer := mustUser(t, s, ctx, "issuer")
	if err := s.SetUserInvitePermission(ctx, issuer.ID, true); err != nil {
		t.Fatal(err)
	}
	generated := invites.Generate()
	invitation, err := s.CreateInvite(ctx, issuer.ID, "friends", generated.Prefix, generated.Hash, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, ctx, issuer, invitation
}

func TestInviteWritesRecheckIssuerPermission(t *testing.T) {
	for _, state := range []string{"permission removed", "disabled"} {
		t.Run(state, func(t *testing.T) {
			s, ctx, issuer, invitation := invitationFixture(t)
			if state == "disabled" {
				if err := s.SetUserDisabled(ctx, issuer.ID, true); err != nil {
					t.Fatal(err)
				}
			} else if err := s.SetUserInvitePermission(ctx, issuer.ID, false); err != nil {
				t.Fatal(err)
			}
			generated := invites.Generate()
			if _, err := s.CreateInvite(ctx, issuer.ID, "stale grant", generated.Prefix, generated.Hash, 1, nil); !errors.Is(err, ErrNotFound) {
				t.Fatalf("creation with stale permission: %v", err)
			}
			if err := s.RevokeInviteByCreator(ctx, invitation.ID, issuer.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("revocation with stale permission: %v", err)
			}
			after, err := s.InviteByID(ctx, invitation.ID)
			// Removing the permission revokes what was issued under it;
			// disabling suspends it, so the code is left as it was.
			if err != nil || after.Revoked() != (state == "permission removed") || after.Uses != 0 {
				t.Fatalf("denied writes changed invitation: %+v, %v", after, err)
			}
			_, count, err := s.ListInvites(ctx, 10, 0)
			if err != nil || count != 1 {
				t.Fatalf("denied creation left data: %d, %v", count, err)
			}
		})
	}
}

func TestDisabledIssuerCannotAdmitPasswordOrProvider(t *testing.T) {
	for _, provider := range []bool{false, true} {
		t.Run(fmt.Sprintf("provider=%t", provider), func(t *testing.T) {
			s, ctx, issuer, invitation := invitationFixture(t)
			if err := s.SetUserDisabled(ctx, issuer.ID, true); err != nil {
				t.Fatal(err)
			}
			in := NewUser{Username: "newcomer", PasswordHash: "hash"}
			register := func() (*models.User, error) {
				if provider {
					return s.CreateUserWithIdentity(ctx, in, "https://id.example", "subject", "", &invitation.ID)
				}
				return s.RegisterWithInvite(ctx, in, invitation.ID)
			}
			if _, err := register(); !errors.Is(err, ErrInviteUnusable) {
				t.Fatalf("disabled issuer admitted account: %v", err)
			}
			if _, err := s.UserByUsername(ctx, in.Username); !errors.Is(err, ErrNotFound) {
				t.Fatalf("denied registration left account: %v", err)
			}
			if _, err := s.IdentityBySubject(ctx, "https://id.example", "subject"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("denied registration left identity: %v", err)
			}
			after, err := s.InviteByID(ctx, invitation.ID)
			if err != nil || after.Uses != 0 {
				t.Fatalf("denied registration consumed code: %+v, %v", after, err)
			}
			if err := s.SetUserDisabled(ctx, issuer.ID, false); err != nil {
				t.Fatal(err)
			}
			joined, err := register()
			if err != nil {
				t.Fatal(err)
			}
			if joined.InvitedBy == nil || *joined.InvitedBy != issuer.ID || joined.InvitedByUsername != issuer.Username || joined.InvitationID == nil || *joined.InvitationID != invitation.ID || joined.CanInvite || joined.Role != models.RoleMember {
				t.Fatalf("wrong invitation attribution or inherited authority: %+v", joined)
			}
		})
	}
}

func TestInvitePermissionRemovalRevokesIssuedCodes(t *testing.T) {
	s, ctx, issuer, invitation := invitationFixture(t)
	if err := s.SetUserInvitePermission(ctx, issuer.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterWithInvite(ctx, NewUser{Username: "friend"}, invitation.ID); !errors.Is(err, ErrInviteUnusable) {
		t.Fatalf("a code outlived its issuer's permission: %v", err)
	}
	// Granting the permission again does not resurrect what was revoked.
	if err := s.SetUserInvitePermission(ctx, issuer.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterWithInvite(ctx, NewUser{Username: "friend"}, invitation.ID); !errors.Is(err, ErrInviteUnusable) {
		t.Fatalf("a revoked code came back with the permission: %v", err)
	}
}

func TestMissingInvitationCannotLeavePasswordOrProviderAccount(t *testing.T) {
	s, ctx := newTestStore(t)
	id := int64(999999)
	in := NewUser{Username: "missing"}
	if _, err := s.RegisterWithInvite(ctx, in, id); !errors.Is(err, ErrInviteUnusable) {
		t.Fatalf("missing password invitation: %v", err)
	}
	if _, err := s.CreateUserWithIdentity(ctx, in, "https://id.example", "missing", "", &id); !errors.Is(err, ErrInviteUnusable) {
		t.Fatalf("missing provider invitation: %v", err)
	}
	if _, err := s.UserByUsername(ctx, in.Username); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing invitation left account: %v", err)
	}
}

func TestInvitePermissionSchemaAndMissingAccounts(t *testing.T) {
	s, ctx, issuer, _ := invitationFixture(t)
	for _, value := range []int{-1, 2, 100} {
		if _, err := s.db.ExecContext(ctx, "UPDATE users SET can_invite = ? WHERE id = ?", value, issuer.ID); err == nil {
			t.Fatalf("schema accepted permission %d", value)
		}
	}
	if err := s.SetUserInvitePermission(ctx, 999999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account reported success: %v", err)
	}
	admin, err := s.CreateUser(ctx, NewUser{Username: "admin", Role: models.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserInvitePermission(ctx, admin.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("administrator's implicit permission was mutated: %v", err)
	}
	after, err := s.UserByID(ctx, issuer.ID)
	if err != nil || !after.CanInvite {
		t.Fatalf("invalid updates changed permission: %+v, %v", after, err)
	}
}

func TestInviteUseLimitsAndAttributionProperty(t *testing.T) {
	for limit := 0; limit <= 3; limit++ {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			s, ctx, issuer, _ := invitationFixture(t)
			if limit == 0 {
				// Only an administrator may issue a code with no limit.
				var err error
				if issuer, err = s.CreateUser(ctx, NewUser{Username: "admin", Role: models.RoleAdmin}); err != nil {
					t.Fatal(err)
				}
			}
			generated := invites.Generate()
			invitation, err := s.CreateInvite(ctx, issuer.ID, "bounded", generated.Prefix, generated.Hash, limit, nil)
			if err != nil {
				t.Fatal(err)
			}
			successes := 0
			for attempt := 0; attempt < 5; attempt++ {
				user, err := s.RegisterWithInvite(ctx, NewUser{Username: fmt.Sprintf("friend%d", attempt)}, invitation.ID)
				allowed := limit == 0 || attempt < limit
				if allowed {
					if err != nil || user.InvitedBy == nil || *user.InvitedBy != issuer.ID || user.CanInvite {
						t.Fatalf("allowed redemption: %+v, %v", user, err)
					}
					successes++
				} else if !errors.Is(err, ErrInviteUnusable) {
					t.Fatalf("spent code accepted: %v", err)
				}
			}
			after, err := s.InviteByID(ctx, invitation.ID)
			if err != nil || after.Uses != successes {
				t.Fatalf("uses do not match successful accounts: %+v, %d, %v", after, successes, err)
			}
		})
	}
}

func TestConcurrentLastInviteUseIsAtomic(t *testing.T) {
	s, ctx, issuer, _ := invitationFixture(t)
	generated := invites.Generate()
	invitation, err := s.CreateInvite(ctx, issuer.ID, "one", generated.Prefix, generated.Hash, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-start
			_, err := s.RegisterWithInvite(ctx, NewUser{Username: fmt.Sprintf("racer%d", i)}, invitation.ID)
			results <- err
		}(i)
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			success++
		} else if !errors.Is(err, ErrInviteUnusable) {
			t.Fatal(err)
		}
	}
	after, err := s.InviteByID(ctx, invitation.ID)
	if err != nil || success != 1 || after.Uses != 1 {
		t.Fatalf("last use was not atomic: successes=%d, %+v, %v", success, after, err)
	}
}
