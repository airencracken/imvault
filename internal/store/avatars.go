// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/airencracken/comfylib/profileimage"
)

func (s *Store) SaveAvatar(ctx context.Context, id int64, data []byte) error {
	picture, err := profileimage.Normalize(data)
	if err != nil {
		return err
	}
	animation := picture.Animation
	if animation == nil {
		animation = []byte{}
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO user_avatars(user_id,still,animation) SELECT id,?,? FROM users WHERE id=? AND disabled=0
 ON CONFLICT(user_id) DO UPDATE SET still=excluded.still,animation=excluded.animation`, picture.Still, animation, id)
	return avatarMutation(result, err)
}

func avatarMutation(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err == nil && rows != 1 {
		return ErrNotFound
	}
	return err
}

func (s *Store) Avatar(ctx context.Context, id int64) (profileimage.Picture, error) {
	var picture profileimage.Picture
	err := s.db.QueryRowContext(ctx, `SELECT a.still,a.animation FROM user_avatars a JOIN users u ON u.id=a.user_id WHERE u.id=? AND u.disabled=0`, id).Scan(&picture.Still, &picture.Animation)
	return picture, mapErr(err)
}

func (s *Store) DeleteAvatar(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM user_avatars WHERE user_id=?", id)
	return err
}

func (s *Store) AnimateAvatars(ctx context.Context, id int64) (bool, error) {
	var on bool
	err := s.db.QueryRowContext(ctx, `SELECT coalesce(p.animate,1) FROM users u LEFT JOIN avatar_preferences p ON p.user_id=u.id WHERE u.id=? AND u.disabled=0`, id).Scan(&on)
	return on, mapErr(err)
}

func (s *Store) SetAnimateAvatars(ctx context.Context, id int64, on bool) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO avatar_preferences(user_id,animate) SELECT id,? FROM users WHERE id=? AND disabled=0
 ON CONFLICT(user_id) DO UPDATE SET animate=excluded.animate`, on, id)
	return avatarMutation(result, err)
}

// AvatarExport deliberately returns only the requesting account's renditions.
func (s *Store) AvatarExport(ctx context.Context, id int64) (profileimage.Picture, bool, error) {
	on, err := s.AnimateAvatars(ctx, id)
	if err != nil {
		return profileimage.Picture{}, false, err
	}
	picture, err := s.Avatar(ctx, id)
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	return picture, on, err
}
