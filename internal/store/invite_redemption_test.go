// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"

	"imvault/internal/invites"
)

func TestInviteForRedemptionReturnsTheRowAndItsDigest(t *testing.T) {
	s, ctx := newTestStore(t)
	boss := mustUser(t, s, ctx, "boss")
	if err := s.SetUserInvitePermission(ctx, boss.ID, true); err != nil {
		t.Fatal(err)
	}
	generated := invites.Generate()
	created, err := s.CreateInvite(ctx, boss.ID, "label", generated.Prefix, generated.Hash, 3, nil)
	if err != nil {
		t.Fatal(err)
	}

	inv, hash, err := s.InviteForRedemption(ctx, generated.Prefix)
	if err != nil {
		t.Fatal(err)
	}
	if inv.ID != created.ID || inv.MaxUses != 3 || inv.Creator != "boss" {
		t.Errorf("invitation = %+v", inv)
	}
	if hash != generated.Hash || !invites.Verify(generated.Full, hash) {
		t.Error("the digest does not verify the code it was made for")
	}

	if _, _, err := s.InviteForRedemption(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown prefix = %v, want ErrNotFound", err)
	}
}
