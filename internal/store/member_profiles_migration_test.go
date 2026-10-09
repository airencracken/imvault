// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"imvault/internal/db"
	"testing"
)

func TestMemberProfileMigrationPreservesAccounts(t *testing.T) {
	s, ctx := newTestStore(t)
	id := mustUser(t, s, ctx, "alice").ID
	var path, beforeName, beforeHash string
	if err := s.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT username,password_hash FROM users WHERE id=?", id).Scan(&beforeName, &beforeHash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TABLE member_profiles; DELETE FROM schema_migrations WHERE version='031_member_profiles.sql'"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	upgraded := New(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upgraded.db.Close() })
	var name, hash string
	if err := upgraded.db.QueryRow("SELECT username,password_hash FROM users WHERE id=?", id).Scan(&name, &hash); err != nil || name != beforeName || hash != beforeHash {
		t.Fatal("upgrade changed account identity", name, err)
	}
	p, err := upgraded.MemberProfile(ctx, id)
	if err != nil || p.Username != "alice" || p.Biography.Name != "" || p.Biography.Bio != "" || len(p.Biography.Links) != 0 {
		t.Fatal("upgrade populated optional profile fields", p, err)
	}
	var profiles int
	if err = upgraded.db.QueryRow("SELECT count(*) FROM member_profiles").Scan(&profiles); err != nil || profiles != 0 {
		t.Fatal("upgrade created unwanted biographies", profiles, err)
	}
}
