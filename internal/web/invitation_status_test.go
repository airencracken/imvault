// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"testing"
	"time"

	"imvault/internal/models"
)

func TestInvitationStatusAdapter(t *testing.T) {
	now := time.Unix(100, 0)
	future := now.Add(time.Nanosecond)
	for _, tc := range []struct {
		invite models.Invite
		want   string
	}{
		{models.Invite{Uses: 10}, "open"},
		{models.Invite{ExpiresAt: &future}, "open"},
		{models.Invite{ExpiresAt: &now}, "expired"},
		{models.Invite{MaxUses: 1, Uses: 1}, "used up"},
		{models.Invite{RevokedAt: &now, ExpiresAt: &now, MaxUses: 1, Uses: 1}, "revoked"},
	} {
		if got := inviteStatus(&tc.invite, now); got != tc.want {
			t.Fatalf("%+v: got %q, want %q", tc.invite, got, tc.want)
		}
	}
}
