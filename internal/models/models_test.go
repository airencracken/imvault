// SPDX-License-Identifier: AGPL-3.0-or-later

package models

import (
	"fmt"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTruncateIsRuneSafe(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		max   int
		want  string
		valid bool
	}{
		{"ascii short enough", "hello", 10, "hello", true},
		{"ascii truncated", "hello world", 5, "hello", true},
		{"zero max", "hello", 0, "hello", true},
		{"multibyte truncated", "日本語テキスト", 3, "日本語", true},
		// This emoji is two runes: U+1F5BC plus a variation selector.
		{"emoji truncated", "🖼️🖼️🖼️", 2, "🖼️", true},
		{"emoji boundary", "🖼️🖼️🖼️", 4, "🖼️🖼️", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Truncate(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("Truncate(%q, %d) produced invalid UTF-8", tc.in, tc.max)
			}
		})
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{3 * 1024 * 1024 * 1024, "3.0 GiB"},
	}
	for _, tc := range tests {
		if got := HumanSize(tc.in); got != tc.want {
			t.Errorf("HumanSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFileExpiry(t *testing.T) {
	now := time.Now()

	permanent := &File{}
	if permanent.Expired(now) {
		t.Error("a file without an expiry should never be expired")
	}
	if permanent.ExpiryLabel() != "" {
		t.Errorf("ExpiryLabel() = %q, want empty", permanent.ExpiryLabel())
	}

	past := now.Add(-time.Minute)
	expired := &File{ExpiresAt: &past}
	if !expired.Expired(now) {
		t.Error("a file past its deadline should be expired")
	}
	if expired.ExpiryLabel() != "expired" {
		t.Errorf("ExpiryLabel() = %q, want expired", expired.ExpiryLabel())
	}

	future := now.Add(90 * time.Minute)
	pending := &File{ExpiresAt: &future}
	if pending.Expired(now) {
		t.Error("a file before its deadline should not be expired")
	}
	if got, want := pending.ExpiryLabel(), "expires in 1h"; got != want {
		t.Errorf("ExpiryLabel() = %q, want %q", got, want)
	}
}

func TestFileHelpers(t *testing.T) {
	id := int64(7)
	owned := &File{UserID: &id, Width: 1920, Height: 1080, Size: 2048}
	if owned.Anonymous() {
		t.Error("a file with an owner is not anonymous")
	}
	if got, want := owned.Dimensions(), "1920×1080"; got != want {
		t.Errorf("Dimensions() = %q, want %q", got, want)
	}

	anon := &File{}
	if !anon.Anonymous() {
		t.Error("a file without an owner is anonymous")
	}
	if anon.Dimensions() != "" {
		t.Errorf("Dimensions() = %q, want empty for unknown size", anon.Dimensions())
	}
}

func ExampleHumanSize() {
	fmt.Println(HumanSize(2048))
	// Output: 2.0 KiB
}

func TestVisibilityParsingClosesOnAnythingUnknown(t *testing.T) {
	// A typo, an empty value, and a level from some future version all become
	// private. The safe direction to be wrong in is closed.
	for _, raw := range []string{"", "  ", "semi-public", "true", "1", "PRIVATEISH"} {
		if got := ParseVisibility(raw); got != VisibilityPrivate {
			t.Errorf("ParseVisibility(%q) = %q, want private", raw, got)
		}
	}

	// Case and surrounding space are the caller's sloppiness, not a level.
	for raw, want := range map[string]Visibility{
		"public":   VisibilityPublic,
		"PUBLIC":   VisibilityPublic,
		" Members": VisibilityMembers,
		"private ": VisibilityPrivate,
	} {
		if got := ParseVisibility(raw); got != want {
			t.Errorf("ParseVisibility(%q) = %q, want %q", raw, got, want)
		}
	}

	// Every level the picker offers must round-trip through the parser, or the
	// form would silently store something other than what was chosen.
	for _, level := range VisibilityLevels() {
		if !level.Valid() {
			t.Errorf("%q is offered but not valid", level)
		}
		if got := ParseVisibility(string(level)); got != level {
			t.Errorf("%q round-tripped to %q", level, got)
		}
	}

	if Visibility("wat").Valid() {
		t.Error("an invented level was accepted")
	}
	if len(VisibilityLevels()) != 3 {
		t.Errorf("%d levels, want 3", len(VisibilityLevels()))
	}
}

func TestAlbumAccessDefaultsToTheOwner(t *testing.T) {
	// Anything unrecognised is the closed level, because an album that quietly
	// accepted anybody's files would be a surprise.
	for _, raw := range []string{"", "  ", "everyone", "public", "true"} {
		if got := ParseAlbumAccess(raw); got != AlbumAccessOwner {
			t.Errorf("ParseAlbumAccess(%q) = %q, want owner", raw, got)
		}
	}

	for raw, want := range map[string]AlbumAccess{
		"members": AlbumAccessMembers,
		"MEMBERS": AlbumAccessMembers,
		" owner ": AlbumAccessOwner,
	} {
		if got := ParseAlbumAccess(raw); got != want {
			t.Errorf("ParseAlbumAccess(%q) = %q, want %q", raw, got, want)
		}
	}

	for _, level := range AlbumAccessLevels() {
		if !level.Valid() {
			t.Errorf("%q is offered but not valid", level)
		}
		if got := ParseAlbumAccess(string(level)); got != level {
			t.Errorf("%q round-tripped to %q", level, got)
		}
	}

	if AlbumAccess("anybody").Valid() {
		t.Error("an invented access level was accepted")
	}
	if !AlbumAccessMembers.Shared() || AlbumAccessOwner.Shared() {
		t.Error("Shared() disagrees with the levels")
	}
}

func TestRoleDefaultsToThePowerlessOne(t *testing.T) {
	// Anything unrecognised is an ordinary member. Getting this wrong in the
	// other direction would hand out moderation, so the zero value and every
	// typo land here.
	for _, raw := range []string{"", "  ", "administrator", "root", "mod", "true"} {
		if got := ParseRole(raw); got != RoleMember {
			t.Errorf("ParseRole(%q) = %q, want member", raw, got)
		}
	}

	for raw, want := range map[string]Role{
		"admin":      RoleAdmin,
		"ADMIN":      RoleAdmin,
		" Moderator": RoleModerator,
		"member ":    RoleMember,
	} {
		if got := ParseRole(raw); got != want {
			t.Errorf("ParseRole(%q) = %q, want %q", raw, got, want)
		}
	}

	if Role("owner").Valid() {
		t.Error("an invented role was accepted")
	}

	// The powers are cumulative, and only administrator is implied downward.
	if !RoleAdmin.CanModerate() || !RoleModerator.CanModerate() || RoleMember.CanModerate() {
		t.Error("CanModerate disagrees with the roles")
	}
	if !RoleAdmin.IsAdmin() || RoleModerator.IsAdmin() || RoleMember.IsAdmin() {
		t.Error("IsAdmin disagrees with the roles")
	}

	for _, role := range RoleLevels() {
		if !role.Valid() {
			t.Errorf("%q is offered but not valid", role)
		}
		if got := ParseRole(string(role)); got != role {
			t.Errorf("%q round-tripped to %q", role, got)
		}
		if role.Label() == "" || role.Explain() == "" {
			t.Errorf("%q has no label or explanation to show", role)
		}
	}

	// A nil account is powerless, which matters because handlers reach here
	// with whatever currentUser returned.
	var nobody *User
	if nobody.IsAdmin() || nobody.CanModerate() {
		t.Error("a nil account has powers")
	}
}
