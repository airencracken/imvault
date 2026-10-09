// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"github.com/airencracken/comfylib/memberprofile"
	"reflect"
	"strings"
	"testing"
)

func TestMemberProfileStorageAtomicitySchemaAndProperties(t *testing.T) {
	s, ctx := newTestStore(t)
	a, b := mustUser(t, s, ctx, "alice").ID, mustUser(t, s, ctx, "bobby").ID
	empty, err := s.MemberProfile(ctx, a)
	if err != nil || empty.Username != "alice" || empty.Biography.Name != "" || len(empty.Biography.Links) != 0 {
		t.Fatal("optional defaults", empty, err)
	}
	original := memberprofile.Profile{Name: "A name", Bio: "A bio", Links: []memberprofile.Link{{Label: "Site", URL: "https://example.org/"}}}
	if err = s.SetMemberProfile(ctx, a, original); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []memberprofile.Profile{{Name: strings.Repeat("x", 81)}, {Bio: strings.Repeat("é", 1001)}, {Links: []memberprofile.Link{{URL: "javascript:bad"}}}, {Links: make([]memberprofile.Link, 6)}} {
		if err = s.SetMemberProfile(ctx, a, bad); err == nil {
			t.Fatal("invalid edit accepted", bad)
		}
		after, err := s.MemberProfile(ctx, a)
		if err != nil || !reflect.DeepEqual(after.Biography, original) {
			t.Fatal("invalid edit changed profile", after, err)
		}
	}
	if _, err = s.db.Exec(`CREATE TRIGGER reject_profile BEFORE UPDATE ON member_profiles BEGIN SELECT RAISE(ABORT,'forced profile failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.SetMemberProfile(ctx, a, memberprofile.Profile{Name: "Other", Bio: "Other", Links: []memberprofile.Link{}}); err == nil {
		t.Fatal("injected failure accepted")
	}
	after, err := s.MemberProfile(ctx, a)
	if err != nil || !reflect.DeepEqual(after.Biography, original) {
		t.Fatal("partial profile update", after, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER reject_profile"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"UPDATE member_profiles SET name='" + strings.Repeat("x", 81) + "'", "UPDATE member_profiles SET bio='" + strings.Repeat("x", 1001) + "'", "UPDATE member_profiles SET links='{}'", "UPDATE member_profiles SET links='not-json'", "UPDATE member_profiles SET links='[1,2,3,4,5,6]'", "INSERT INTO member_profiles(user_id) VALUES(99999)"} {
		if _, err = s.db.Exec(query); err == nil {
			t.Fatal("schema accepted invalid profile", query)
		}
	}
	for count := 0; count <= memberprofile.MaxLinks; count++ {
		p := memberprofile.Profile{Name: strings.Repeat("界", 80), Bio: strings.Repeat("é", 1000), Links: []memberprofile.Link{}}
		for i := 0; i < count; i++ {
			p.Links = append(p.Links, memberprofile.Link{URL: "https://example.org/"})
		}
		if err = s.SetMemberProfile(ctx, a, p); err != nil {
			t.Fatal(err)
		}
		got, err := s.MemberProfile(ctx, a)
		if err != nil || !reflect.DeepEqual(got.Biography, p) {
			t.Fatal("valid profile did not round-trip", count, got, err)
		}
		other, err := s.MemberProfile(ctx, b)
		if err != nil || other.Biography.Name != "" || other.Biography.Bio != "" || len(other.Biography.Links) != 0 {
			t.Fatal("profile edit changed another member", other, err)
		}
	}
	if err = s.SetMemberProfile(ctx, a, memberprofile.Profile{}); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.MemberProfile(ctx, a)
	if err != nil || cleared.Biography.Name != "" || cleared.Biography.Bio != "" || len(cleared.Biography.Links) != 0 {
		t.Fatal("clearing optional fields failed", cleared, err)
	}
	if _, err = s.db.Exec("UPDATE member_profiles SET links='[1]' WHERE user_id=?", a); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MemberProfile(ctx, a); err == nil {
		t.Fatal("corrupt profile did not fail closed")
	}
	if err = s.SetMemberProfile(ctx, a, original); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE users SET disabled=1 WHERE id=?", a); err != nil {
		t.Fatal(err)
	}
	if err = s.SetMemberProfile(ctx, a, original); err == nil {
		t.Fatal("inactive account changed profile")
	}
	if _, err = s.MemberProfile(ctx, a); err == nil {
		t.Fatal("inactive profile exposed")
	}
	if err = s.SetMemberProfile(ctx, 99999, original); err == nil {
		t.Fatal("missing account changed profile")
	}
	if _, err = s.MemberProfile(ctx, 99999); err == nil {
		t.Fatal("missing account has profile")
	}
	if err = s.SetMemberProfile(ctx, b, original); err != nil {
		t.Fatal(err)
	}
	err = s.DeleteUser(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	var rows int
	if err = s.db.QueryRow("SELECT count(*) FROM member_profiles WHERE user_id=?", b).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("deleted account retained profile", rows, err)
	}
	if err = s.SetMemberProfile(ctx, b, original); err == nil {
		t.Fatal("late write recreated deleted account profile")
	}
}
