// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"

	"imvault/internal/apikeys"
	"imvault/internal/models"
)

func TestAPIKeyLifecycle(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")

	gen := apikeys.Generate()
	key, err := s.CreateAPIKey(ctx, alice.ID, "laptop", gen.Prefix, gen.Hash, nil)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	if key.Prefix != gen.Prefix {
		t.Errorf("prefix = %q, want %q", key.Prefix, gen.Prefix)
	}
	if key.ExpiresAt != nil {
		t.Error("a key with no expiry should have a nil ExpiresAt")
	}

	// The auth path looks a key up by its public prefix.
	found, err := s.APIKeyByPrefix(ctx, gen.Prefix)
	if err != nil {
		t.Fatalf("lookup api key: %v", err)
	}
	if !apikeys.Verify(gen.Full, found.KeyHash) {
		t.Error("the stored hash does not verify the generated key")
	}
	if found.UserID != alice.ID {
		t.Errorf("key owner = %d, want %d", found.UserID, alice.ID)
	}
	if found.Username != "alice" {
		t.Errorf("joined username = %q, want alice", found.Username)
	}

	if _, err := s.APIKeyByPrefix(ctx, "nosuchprefix"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown prefix error = %v, want ErrNotFound", err)
	}

	if err := s.TouchAPIKey(ctx, key.ID, time.Now()); err != nil {
		t.Fatalf("touch: %v", err)
	}
	keys, err := s.APIKeysByUser(ctx, alice.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("alice has %d keys, want 1", len(keys))
	}
	if keys[0].LastUsedAt == nil {
		t.Error("LastUsedAt was not recorded")
	}

	// One account must not be able to revoke another's key.
	if err := s.DeleteAPIKey(ctx, key.ID, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user delete error = %v, want ErrNotFound", err)
	}
	if _, err := s.APIKeyByPrefix(ctx, gen.Prefix); err != nil {
		t.Fatalf("key was removed by the wrong user: %v", err)
	}

	if err := s.DeleteAPIKey(ctx, key.ID, alice.ID); err != nil {
		t.Fatalf("delete own key: %v", err)
	}
	if _, err := s.APIKeyByPrefix(ctx, gen.Prefix); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete error = %v, want ErrNotFound", err)
	}
}

func TestAPIKeyExpiry(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)

	stale := apikeys.Generate()
	live := apikeys.Generate()
	if _, err := s.CreateAPIKey(ctx, alice.ID, "stale", stale.Prefix, stale.Hash, &past); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAPIKey(ctx, alice.ID, "live", live.Prefix, live.Hash, &future); err != nil {
		t.Fatal(err)
	}

	// Newest first, with the id breaking ties when two keys share a timestamp.
	keys, err := s.APIKeysByUser(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	var staleKey, liveKey *models.APIKey
	for _, k := range keys {
		switch k.Prefix {
		case stale.Prefix:
			staleKey = k
		case live.Prefix:
			liveKey = k
		}
	}
	if staleKey == nil || liveKey == nil {
		t.Fatalf("expected both keys, got %d", len(keys))
	}

	if !staleKey.Expired(time.Now()) {
		t.Error("the past-dated key should report as expired")
	}
	if liveKey.Expired(time.Now()) {
		t.Error("the future-dated key should not report as expired")
	}

	pruned, err := s.DeleteExpiredAPIKeys(ctx, time.Now())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned %d keys, want 1", pruned)
	}
	if _, err := s.APIKeyByPrefix(ctx, stale.Prefix); !errors.Is(err, ErrNotFound) {
		t.Error("the expired key survived the prune")
	}
	if _, err := s.APIKeyByPrefix(ctx, live.Prefix); err != nil {
		t.Errorf("the live key was pruned: %v", err)
	}
}

func TestDeletingAUserRemovesTheirKeys(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	gen := apikeys.Generate()
	if _, err := s.CreateAPIKey(ctx, alice.ID, "laptop", gen.Prefix, gen.Hash, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DB().ExecContext(ctx, `DELETE FROM users WHERE id = ?`, alice.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	if _, err := s.APIKeyByPrefix(ctx, gen.Prefix); !errors.Is(err, ErrNotFound) {
		t.Errorf("key outlived its owner: %v", err)
	}
}
