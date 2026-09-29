package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"imvault/internal/models"
)

func TestSetUserRoleRejectsMissingUser(t *testing.T) {
	s, ctx := newTestStore(t)
	for _, role := range []models.Role{models.RoleAdmin, models.RoleModerator, models.RoleMember} {
		if err := s.SetUserRole(ctx, 999, role); !errors.Is(err, ErrNotFound) {
			t.Fatalf("set missing user's role to %s = %v", role, err)
		}
	}
}

func TestDisabledAdminCannotSatisfyLastAdminGuard(t *testing.T) {
	for _, action := range []string{"demote", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, ctx := newTestStore(t)
			active := mustUser(t, s, ctx, "active")
			disabled := mustUser(t, s, ctx, "disabled")
			for _, id := range []int64{active.ID, disabled.ID} {
				if err := s.SetUserRole(ctx, id, models.RoleAdmin); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.SetUserDisabled(ctx, disabled.ID, true); err != nil {
				t.Fatal(err)
			}
			var err error
			if action == "demote" {
				err = s.SetUserRole(ctx, active.ID, models.RoleMember)
			} else {
				err = s.DeleteUser(ctx, active.ID)
			}
			if !errors.Is(err, ErrLastAdmin) {
				t.Fatalf("%s last enabled admin = %v; disabled admin incorrectly satisfies guard", action, err)
			}
		})
	}
}

func TestConcurrentAdminRemovalPreservesOneEnabledAdministrator(t *testing.T) {
	for _, action := range []string{"demote", "delete", "disable"} {
		t.Run(action, func(t *testing.T) {
			s, ctx := newTestStore(t)
			ids := []int64{}
			for i := range 12 {
				u := mustUser(t, s, ctx, fmt.Sprintf("admin-%d", i))
				if err := s.SetUserRole(ctx, u.ID, models.RoleAdmin); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, u.ID)
			}
			var wg sync.WaitGroup
			for _, id := range ids {
				wg.Go(func() {
					var err error
					switch action {
					case "demote":
						err = s.SetUserRole(ctx, id, models.RoleMember)
					case "delete":
						err = s.DeleteUser(ctx, id)
					case "disable":
						err = s.SetUserDisabled(ctx, id, true)
					}
					if err != nil && !errors.Is(err, ErrLastAdmin) {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			count, err := s.CountAdmins(ctx)
			if err != nil || count != 1 {
				t.Fatalf("enabled admins = %d, err=%v", count, err)
			}
		})
	}
}
