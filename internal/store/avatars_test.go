// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"reflect"
	"testing"

	"imvault/internal/db"
)

func avatarGIF(t *testing.T, frames, size int) []byte {
	t.Helper()
	v := &gif.GIF{LoopCount: 0}
	for i := 0; i < frames; i++ {
		p := image.NewPaletted(image.Rect(0, 0, size, size), color.Palette{color.Black, color.White})
		p.SetColorIndex(i%size, 0, 1)
		v.Image = append(v.Image, p)
		v.Delay = append(v.Delay, 1)
	}
	var out bytes.Buffer
	if err := gif.EncodeAll(&out, v); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestAvatarStorageAtomicitySchemaAndProperties(t *testing.T) {
	s, ctx := newTestStore(t)
	id := mustUser(t, s, ctx, "alice").ID
	other := mustUser(t, s, ctx, "bobby").ID
	if _, err := s.Avatar(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("invented default avatar", err)
	}
	if on, err := s.AnimateAvatars(ctx, id); err != nil || !on {
		t.Fatal("default preference", on, err)
	}
	valid := avatarGIF(t, 3, 16)
	if err := s.SaveAvatar(ctx, id, valid); err != nil {
		t.Fatal(err)
	}
	before, err := s.Avatar(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{nil, []byte("<svg onload=bad>"), avatarGIF(t, 65, 8), avatarGIF(t, 2, 513), append(append([]byte{}, valid...), 0), valid[:len(valid)-1], make([]byte, (2<<20)+1)} {
		if err := s.SaveAvatar(ctx, id, bad); err == nil {
			t.Fatal("adversarial avatar accepted")
		}
		got, err := s.Avatar(ctx, id)
		if err != nil || !reflect.DeepEqual(got, before) {
			t.Fatal("failed image changed state", err)
		}
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_avatar BEFORE UPDATE ON user_avatars BEGIN SELECT RAISE(ABORT,'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAvatar(ctx, id, avatarGIF(t, 4, 16)); err == nil {
		t.Fatal("injected update succeeded")
	}
	got, err := s.Avatar(ctx, id)
	if err != nil || !reflect.DeepEqual(got, before) {
		t.Fatal("partial replacement", err)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_avatar"); err != nil {
		t.Fatal(err)
	}
	for frames := 1; frames <= 64; frames++ {
		on := frames%2 == 0
		if err := s.SetAnimateAvatars(ctx, id, on); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveAvatar(ctx, id, avatarGIF(t, frames, 8)); err != nil {
			t.Fatal(frames, err)
		}
		p, err := s.Avatar(ctx, id)
		if err != nil || (len(p.Animation) > 0) != (frames > 1) {
			t.Fatal("frame property", frames, err)
		}
		if actual, err := s.AnimateAvatars(ctx, id); err != nil || actual != on {
			t.Fatal("preference changed", frames, actual, err)
		}
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAvatar(ctx, id, pngData.Bytes()); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Avatar(ctx, id); err != nil || len(p.Animation) != 0 {
		t.Fatal("static retained animation", err)
	}
	for _, q := range []string{"UPDATE user_avatars SET still=X'00'", "UPDATE user_avatars SET animation=X'00'", "INSERT INTO avatar_preferences(user_id,animate) VALUES(999999,1)", "UPDATE avatar_preferences SET animate=2", "UPDATE avatar_preferences SET animate=-1"} {
		if _, err := s.db.Exec(q); err == nil {
			t.Fatal("schema accepted", q)
		}
	}
	if _, err := s.Avatar(ctx, other); !errors.Is(err, ErrNotFound) {
		t.Fatal("other member avatar changed", err)
	}
	if err := s.SetAnimateAvatars(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAvatar(ctx, id); err != nil {
		t.Fatal(err)
	}
	if on, err := s.AnimateAvatars(ctx, id); err != nil || on {
		t.Fatal("removal reset preference", err)
	}
	if err := s.SaveAvatar(ctx, id, valid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE users SET disabled=1 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAvatar(ctx, id, valid); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled upload", err)
	}
	if _, err := s.Avatar(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled avatar exposed", err)
	}
	if err := s.SetAnimateAvatars(ctx, id, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled preference", err)
	}
	if err := s.DeleteUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"user_avatars", "avatar_preferences"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM "+table+" WHERE user_id=?", id).Scan(&count); err != nil || count != 0 {
			t.Fatal("deletion retained", table, err)
		}
	}
	if err := s.SaveAvatar(ctx, id, valid); !errors.Is(err, ErrNotFound) {
		t.Fatal("late resurrection", err)
	}
	if err := s.SetAnimateAvatars(ctx, id, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("late preference resurrection", err)
	}
}

func TestAvatarMigrationPreservesExistingProfiles(t *testing.T) {
	s, ctx := newTestStore(t)
	id := mustUser(t, s, ctx, "alice").ID
	var path, hash string
	if err := s.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT password_hash FROM users WHERE id=?", id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO member_profiles(user_id,name,bio,links) VALUES(?,'Name','Bio','[]')", id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TABLE user_avatars; DROP TABLE avatar_preferences; DELETE FROM schema_migrations WHERE version='032_avatars.sql'"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	upgraded := New(database)
	user, err := upgraded.UserByID(ctx, id)
	if err != nil || user.PasswordHash != hash {
		t.Fatal("migration changed credentials", err)
	}
	profile, err := upgraded.MemberProfile(ctx, id)
	if err != nil || profile.Biography.Name != "Name" || profile.Biography.Bio != "Bio" || profile.HasAvatar {
		t.Fatal("migration changed profile", profile, err)
	}
	if on, err := upgraded.AnimateAvatars(ctx, id); err != nil || !on {
		t.Fatal("migration preference", on, err)
	}
}
