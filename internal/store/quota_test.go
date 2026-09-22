// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"imvault/internal/models"
)

func TestConcurrentFileInsertsRespectInstanceCeiling(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "owner")
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			f := &models.File{ID: fmt.Sprintf("file-%d", i), Size: 100, Ext: "png",
				Mime: "image/png", CreatedAt: time.Now()}
			if i%2 == 0 {
				f.UserID = &owner.ID
			}
			results <- s.CreateFileWithLimit(ctx, f, 1000)
		})
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrInstanceFull) {
			t.Fatal(err)
		}
	}
	if accepted != 10 {
		t.Fatalf("accepted %d files, want exactly 10", accepted)
	}
	total, err := s.TotalStoredBytes(ctx)
	if err != nil || total != 1000 {
		t.Fatalf("stored bytes = %d, error=%v", total, err)
	}
}

func TestStorageReservationHonoursQuota(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	// A new account is unlimited, so anything fits.
	if err := s.ReserveStorage(ctx, alice.ID, 10_000, 0); err != nil {
		t.Fatalf("reserve on an unlimited account: %v", err)
	}
	if got := mustLoadUser(t, s, ctx, alice.ID).StorageUsed; got != 10_000 {
		t.Errorf("storage_used = %d, want 10000", got)
	}

	// Lower the cap below current usage: the account is now over quota, and
	// nothing more can be stored.
	if err := s.SetUserQuota(ctx, alice.ID, 1_000); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	user := mustLoadUser(t, s, ctx, alice.ID)
	if !user.OverQuota() {
		t.Error("the account should report as over quota")
	}
	if user.RemainingBytes() != 0 {
		t.Errorf("remaining = %d, want 0", user.RemainingBytes())
	}
	if user.UsagePercent() != 100 {
		t.Errorf("usage = %d%%, want 100", user.UsagePercent())
	}

	if err := s.CheckQuota(ctx, alice.ID, 1, 0); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("check over quota = %v, want ErrQuotaExceeded", err)
	}
	if err := s.ReserveStorage(ctx, alice.ID, 1, 0); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("reserve over quota = %v, want ErrQuotaExceeded", err)
	}

	// Releasing brings it back under, and the boundary is exact.
	if err := s.ReleaseStorage(ctx, alice.ID, 9_500); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := mustLoadUser(t, s, ctx, alice.ID).StorageUsed; got != 500 {
		t.Fatalf("storage_used = %d, want 500", got)
	}
	if err := s.ReserveStorage(ctx, alice.ID, 500, 0); err != nil {
		t.Errorf("reserving exactly the remaining room failed: %v", err)
	}
	if err := s.ReserveStorage(ctx, alice.ID, 1, 0); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("reserving past the boundary = %v, want ErrQuotaExceeded", err)
	}

	// Usage never goes negative, however many times it is released.
	if err := s.ReleaseStorage(ctx, alice.ID, 1_000_000); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := mustLoadUser(t, s, ctx, alice.ID).StorageUsed; got != 0 {
		t.Errorf("storage_used = %d, want it clamped at 0", got)
	}

	// Zero means unlimited again.
	if err := s.SetUserQuota(ctx, alice.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveStorage(ctx, alice.ID, 1<<40, 0); err != nil {
		t.Errorf("an unlimited account was refused: %v", err)
	}
}

func TestConcurrentReservationsCannotOversubscribe(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	// A 1000-byte cap with ten workers each claiming 100 bytes: exactly ten
	// must win, no matter how the scheduling falls.
	if err := s.SetUserQuota(ctx, alice.ID, 1_000); err != nil {
		t.Fatal(err)
	}

	const (
		workers  = 20
		claim    = 100
		expected = 10
	)

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.ReserveStorage(ctx, alice.ID, claim, 0); err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if succeeded != expected {
		t.Errorf("%d reservations succeeded, want %d", succeeded, expected)
	}
	if got := mustLoadUser(t, s, ctx, alice.ID).StorageUsed; got != expected*claim {
		t.Errorf("storage_used = %d, want %d", got, expected*claim)
	}
}

func TestRecomputeStorageUsageRepairsDrift(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	mustFile(t, s, ctx, "one", &alice.ID, models.VisibilityPrivate, nil) // 1024 bytes
	mustFile(t, s, ctx, "two", &alice.ID, models.VisibilityPrivate, nil)

	// Simulate a crash that reserved bytes without recording a file.
	if err := s.ReserveStorage(ctx, alice.ID, 500_000, 0); err != nil {
		t.Fatal(err)
	}
	if got := mustLoadUser(t, s, ctx, alice.ID).StorageUsed; got != 500_000 {
		t.Fatalf("setup: storage_used = %d", got)
	}

	corrected, err := s.RecomputeStorageUsage(ctx)
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if corrected != 1 {
		t.Errorf("corrected %d accounts, want 1", corrected)
	}
	if got := mustLoadUser(t, s, ctx, alice.ID).StorageUsed; got != 2048 {
		t.Errorf("storage_used = %d, want 2048 (two files of 1024)", got)
	}

	// Running it again finds nothing to do.
	corrected, err = s.RecomputeStorageUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if corrected != 0 {
		t.Errorf("second pass corrected %d accounts, want 0", corrected)
	}
}

func TestLastAdministratorCannotBeRemoved(t *testing.T) {
	s, ctx := newTestStore(t)

	boss := mustUser(t, s, ctx, "boss")
	if err := s.SetUserRole(ctx, boss.ID, models.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	if err := s.SetUserRole(ctx, boss.ID, models.RoleMember); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demoting the last admin = %v, want ErrLastAdmin", err)
	}
	if err := s.DeleteUser(ctx, boss.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("deleting the last admin = %v, want ErrLastAdmin", err)
	}

	// With a second administrator, removal becomes possible.
	deputy := mustUser(t, s, ctx, "deputy")
	if err := s.SetUserRole(ctx, deputy.ID, models.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, boss.ID); err != nil {
		t.Errorf("deleting an admin while another exists: %v", err)
	}

	// Now the deputy is the last one, and is protected again.
	if err := s.SetUserRole(ctx, deputy.ID, models.RoleMember); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demoting the remaining admin = %v, want ErrLastAdmin", err)
	}
	if err := s.DeleteUser(ctx, deputy.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("deleting the remaining admin = %v, want ErrLastAdmin", err)
	}
}

func TestDeletedUserLeavesNoFilesKeysOrTags(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	mustFile(t, s, ctx, "pic", &alice.ID, models.VisibilityPrivate, nil)
	if _, err := s.AddTag(ctx, "pic", &alice.ID, "beach"); err != nil {
		t.Fatal(err)
	}
	gen := "prefix000000"
	if _, err := s.CreateAPIKey(ctx, alice.ID, "laptop", gen, "hash", nil); err != nil {
		t.Fatal(err)
	}

	// The content is recorded before the account goes, so the bytes can be
	// attributed afterwards.
	blob, err := s.BlobBySHA(ctx, "deadbeefpic")
	if err != nil {
		t.Fatalf("blob for pic: %v", err)
	}
	if blob.Refcount != 1 {
		t.Fatalf("refcount = %d, want 1 before the account is deleted", blob.Refcount)
	}

	if err := s.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	if _, err := s.UserByID(ctx, alice.ID); !errors.Is(err, ErrNotFound) {
		t.Error("the account survived")
	}
	if _, err := s.FileByID(ctx, "pic"); !errors.Is(err, ErrNotFound) {
		t.Error("the account's file survived")
	}
	if keys, err := s.APIKeysByUser(ctx, alice.ID); err != nil || len(keys) != 0 {
		t.Errorf("api keys survived: %v %v", keys, err)
	}
	tags, err := s.ListTags(ctx, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Errorf("tags survived: %+v", tags)
	}
}

func TestInstanceStats(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")
	if err := s.SetUserRole(ctx, alice.ID, models.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserDisabled(ctx, bob.ID, true); err != nil {
		t.Fatal(err)
	}

	mustFile(t, s, ctx, "pub", &alice.ID, models.VisibilityPublic, nil)
	mustFile(t, s, ctx, "priv", &bob.ID, models.VisibilityPrivate, nil)
	mustFile(t, s, ctx, "anon", nil, models.VisibilityPublic, nil)

	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Users != 2 || stats.Admins != 1 || stats.Disabled != 1 {
		t.Errorf("user counts = %+v", stats)
	}
	if stats.Files != 3 || stats.PublicFiles != 2 {
		t.Errorf("file counts = %+v", stats)
	}
	if stats.TotalBytes != 3*1024 {
		t.Errorf("total bytes = %d, want %d", stats.TotalBytes, 3*1024)
	}
}

func mustLoadUser(t *testing.T, s *Store, ctx context.Context, id int64) *models.User {
	t.Helper()
	user, err := s.UserByID(ctx, id)
	if err != nil {
		t.Fatalf("load user %d: %v", id, err)
	}
	return user
}

func TestTotalStoredBytesCountsAnonymousToo(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	if total, err := s.TotalStoredBytes(ctx); err != nil || total != 0 {
		t.Fatalf("empty instance total = %d (%v), want 0", total, err)
	}

	// mustFile writes 1024 bytes each, and the anonymous one has no account to
	// be counted under. A ceiling that ignored anonymous uploads would be no
	// ceiling at all on a public instance, where they are the bulk of the
	// traffic.
	mustFile(t, s, ctx, "owned", &alice.ID, models.VisibilityPrivate, nil)
	mustFile(t, s, ctx, "anon", nil, models.VisibilityPublic, nil)

	total, err := s.TotalStoredBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2048 {
		t.Errorf("total = %d, want 2048 including the anonymous upload", total)
	}

	// It is derived, so it follows a delete without anything having to remember
	// to decrement it.
	if err := s.DeleteFile(ctx, "anon"); err != nil {
		t.Fatal(err)
	}
	if total, err := s.TotalStoredBytes(ctx); err != nil || total != 1024 {
		t.Errorf("total after a delete = %d (%v), want 1024", total, err)
	}
}

func TestCheckQuotaRefusesAtTheInstanceCeiling(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")
	mustFile(t, s, ctx, "one", &alice.ID, models.VisibilityPrivate, nil) // 1024 bytes

	// A ceiling of exactly the total plus one more file.
	if err := s.CheckQuota(ctx, alice.ID, 1024, 2048); err != nil {
		t.Errorf("a file that just fits was refused: %v", err)
	}
	if err := s.CheckQuota(ctx, alice.ID, 1025, 2048); !errors.Is(err, ErrInstanceFull) {
		t.Errorf("a file past the ceiling = %v, want ErrInstanceFull", err)
	}

	// Zero means the operator set no ceiling, and then nothing here applies.
	if err := s.CheckQuota(ctx, alice.ID, 1<<40, 0); err != nil {
		t.Errorf("an instance with no ceiling refused a file: %v", err)
	}

	// The instance ceiling and the account quota are different refusals, and the
	// message depends on telling them apart.
	if err := s.SetUserQuota(ctx, alice.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckQuota(ctx, alice.ID, 200, 0); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("an account over its own quota = %v, want ErrQuotaExceeded", err)
	}
}

func TestReserveStorageNamesWhichCeilingWasHit(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	// The account's own cap, with no instance ceiling in play.
	if err := s.SetUserQuota(ctx, alice.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveStorage(ctx, alice.ID, 200, 0); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("account cap = %v, want ErrQuotaExceeded", err)
	}

	// The instance ceiling, with the account unlimited.
	if err := s.SetUserQuota(ctx, alice.ID, 0); err != nil {
		t.Fatal(err)
	}
	mustFile(t, s, ctx, "one", &alice.ID, models.VisibilityPrivate, nil)
	if err := s.ReserveStorage(ctx, alice.ID, 2000, 2048); !errors.Is(err, ErrInstanceFull) {
		t.Errorf("instance ceiling = %v, want ErrInstanceFull", err)
	}

	// And it still reserves when there is room, so the ceiling is a ceiling and
	// not a blanket refusal.
	if err := s.ReserveStorage(ctx, alice.ID, 1024, 2048); err != nil {
		t.Errorf("a file that fits was refused: %v", err)
	}
}
