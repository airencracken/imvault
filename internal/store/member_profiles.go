// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"encoding/json"

	"github.com/airencracken/comfylib/memberprofile"
)

// MemberProfile omits email, credentials, quotas and private account settings.
type MemberProfile struct {
	HasAvatar bool
	ID        int64
	Username  string
	Biography memberprofile.Profile
}

func (s *Store) MemberProfile(ctx context.Context, id int64) (MemberProfile, error) {
	var p MemberProfile
	var links string
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,coalesce(p.name,''),coalesce(p.bio,''),coalesce(p.links,'[]'),EXISTS(SELECT 1 FROM user_avatars a WHERE a.user_id=u.id) FROM users u LEFT JOIN member_profiles p ON p.user_id=u.id WHERE u.id=? AND u.disabled=0`, id).Scan(&p.ID, &p.Username, &p.Biography.Name, &p.Biography.Bio, &links, &p.HasAvatar)
	if err != nil {
		return p, mapErr(err)
	}
	if err = json.Unmarshal([]byte(links), &p.Biography.Links); err != nil {
		return p, err
	}
	p.Biography, err = memberprofile.Normalize(p.Biography)
	return p, err
}

func (s *Store) SetMemberProfile(ctx context.Context, id int64, p memberprofile.Profile) error {
	p, err := memberprofile.Normalize(p)
	if err != nil {
		return err
	}
	links, err := json.Marshal(p.Links)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO member_profiles(user_id,name,bio,links) SELECT id,?,?,? FROM users WHERE id=? AND disabled=0 ON CONFLICT(user_id) DO UPDATE SET name=excluded.name,bio=excluded.bio,links=excluded.links`, p.Name, p.Bio, string(links), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return ErrNotFound
	}
	return err
}
