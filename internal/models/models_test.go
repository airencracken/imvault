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

func TestInviteUsability(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	revoked := now

	tests := map[string]struct {
		invite Invite
		usable bool
	}{
		"a fresh single-use code": {Invite{MaxUses: 1}, true},
		"a spent single-use code": {Invite{MaxUses: 1, Uses: 1}, false},
		"a partly used code":      {Invite{MaxUses: 3, Uses: 1}, true},
		"an unlimited code":       {Invite{MaxUses: 0, Uses: 500}, true},
		"an expired code":         {Invite{MaxUses: 1, ExpiresAt: &past}, false},
		"a code not yet expired":  {Invite{MaxUses: 1, ExpiresAt: &future}, true},
		"a revoked code":          {Invite{MaxUses: 1, RevokedAt: &revoked}, false},
	}

	for name, tc := range tests {
		if got := tc.invite.Usable(now); got != tc.usable {
			t.Errorf("%s: Usable = %v, want %v", name, got, tc.usable)
		}
	}

	// The label has to distinguish "three of five" from "three, ever", because
	// an administrator reads it to decide what to revoke.
	if got := (&Invite{MaxUses: 5, Uses: 3}).UsesLabel(); got != "3 of 5" {
		t.Errorf("UsesLabel = %q, want \"3 of 5\"", got)
	}
	if got := (&Invite{MaxUses: 0, Uses: 3}).UsesLabel(); got != "3 used" {
		t.Errorf("UsesLabel = %q, want \"3 used\"", got)
	}

	// Remaining never goes negative, so a display cannot read as "-2 left".
	if got := (&Invite{MaxUses: 1, Uses: 4}).Remaining(); got != 0 {
		t.Errorf("Remaining = %d, want 0", got)
	}
	if got := (&Invite{MaxUses: 5, Uses: 2}).Remaining(); got != 3 {
		t.Errorf("Remaining = %d, want 3", got)
	}
	if got := (&Invite{MaxUses: 0, Uses: 2}).Remaining(); got != 0 {
		t.Errorf("Remaining = %d for an unlimited code, want 0", got)
	}
}

func TestReportReasonKeepsTheReport(t *testing.T) {
	// A reason nobody offered is not a reason, but refusing the whole report
	// over it would lose the report, so it becomes "other" rather than failing.
	for _, raw := range []string{"", "  ", "garbage", "SPAMISH"} {
		if got := ParseReportReason(raw); got != ReportOther {
			t.Errorf("ParseReportReason(%q) = %q, want other", raw, got)
		}
	}

	for raw, want := range map[string]ReportReason{
		"spam":       ReportSpam,
		"ABUSE":      ReportAbuse,
		" Copyright": ReportCopyright,
		"illegal ":   ReportIllegal,
	} {
		if got := ParseReportReason(raw); got != want {
			t.Errorf("ParseReportReason(%q) = %q, want %q", raw, got, want)
		}
	}

	// Every offered reason must round-trip and be presentable, or the picker
	// would store something other than what was chosen and then fail to label it.
	for _, reason := range ReportReasons() {
		if !reason.Valid() {
			t.Errorf("%q is offered but not valid", reason)
		}
		if got := ParseReportReason(string(reason)); got != reason {
			t.Errorf("%q round-tripped to %q", reason, got)
		}
		if reason.Label() == "" {
			t.Errorf("%q has no label", reason)
		}
	}

	if ReportReason("nonsense").Valid() {
		t.Error("an invented reason was accepted")
	}
	if len(ReportReasons()) != 5 {
		t.Errorf("%d reasons offered, want 5", len(ReportReasons()))
	}

	// Statuses and actions are only ever shown to people, so they all have to
	// say something.
	for _, status := range []ReportStatus{ReportOpen, ReportActioned, ReportDismissed} {
		if status.Label() == "" {
			t.Errorf("status %q has no label", status)
		}
	}
	for _, action := range []ModerationAction{
		ActionRemoveFile, ActionRemoveAlbum, ActionResolveReport, ActionDismissReport,
	} {
		if action.Label() == "" || action.Label() == string(action) {
			t.Errorf("action %q has no phrase to show", action)
		}
	}
	for _, kind := range []TargetKind{TargetFile, TargetAlbum} {
		if kind.Label() == "" {
			t.Errorf("target kind %q has no label", kind)
		}
	}
	if got := (&Report{Status: ReportOpen}).Open(); !got {
		t.Error("an open report does not report itself as open")
	}
	if got := (&Report{Status: ReportDismissed}).Open(); got {
		t.Error("a dismissed report reports itself as open")
	}
}

func TestMetadataPolicyResolution(t *testing.T) {
	// Anything unrecognised, and the empty value every existing row has, means
	// "follow visibility": deferring is safer than pinning a choice nobody made.
	for _, raw := range []string{"", "  ", "public", "true", "hide"} {
		if got := ParseMetadataPolicy(raw); got != MetadataInherit {
			t.Errorf("ParseMetadataPolicy(%q) = %q, want inherit", raw, got)
		}
	}

	for raw, want := range map[string]MetadataPolicy{
		"hidden":  MetadataHidden,
		"HIDDEN":  MetadataHidden,
		" shown ": MetadataShown,
	} {
		if got := ParseMetadataPolicy(raw); got != want {
			t.Errorf("ParseMetadataPolicy(%q) = %q, want %q", raw, got, want)
		}
	}

	// Inherit is the whole point of the visibility rule: public is the whole
	// internet, everything else is the audience the file was shared with.
	for _, tc := range []struct {
		visibility Visibility
		want       MetadataPolicy
	}{
		{VisibilityPublic, MetadataHidden},
		{VisibilityMembers, MetadataShown},
		{VisibilityPrivate, MetadataShown},
	} {
		if got := MetadataInherit.Resolve(tc.visibility); got != tc.want {
			t.Errorf("inherit on %s resolved to %q, want %q", tc.visibility, got, tc.want)
		}
		// An explicit setting ignores visibility altogether, in both
		// directions. That is what makes it an override.
		if got := MetadataHidden.Resolve(tc.visibility); got != MetadataHidden {
			t.Errorf("hidden on %s resolved to %q", tc.visibility, got)
		}
		if got := MetadataShown.Resolve(tc.visibility); got != MetadataShown {
			t.Errorf("shown on %s resolved to %q", tc.visibility, got)
		}
	}

	for _, policy := range MetadataLevels() {
		if !policy.Valid() {
			t.Errorf("%q is offered but not valid", policy)
		}
		if got := ParseMetadataPolicy(string(policy)); got != policy {
			t.Errorf("%q round-tripped to %q", policy, got)
		}
		if policy.Label() == "" || policy.Explain() == "" {
			t.Errorf("%q has nothing to show", policy)
		}
	}
}

func TestStrictestOnlyEverTightens(t *testing.T) {
	// Combining never yields something more open than any input, which is what
	// lets an album add caution without being able to remove it.
	cases := map[string]struct {
		in   []MetadataPolicy
		want MetadataPolicy
	}{
		"nothing":                 {nil, MetadataInherit},
		"one opinion":             {[]MetadataPolicy{MetadataShown}, MetadataShown},
		"a later hidden wins":     {[]MetadataPolicy{MetadataShown, MetadataHidden}, MetadataHidden},
		"order does not matter":   {[]MetadataPolicy{MetadataHidden, MetadataShown}, MetadataHidden},
		"inherit does not loosen": {[]MetadataPolicy{MetadataHidden, MetadataInherit}, MetadataHidden},
		"inherit does not hide":   {[]MetadataPolicy{MetadataShown, MetadataInherit}, MetadataShown},
		"shown cannot lift":       {[]MetadataPolicy{MetadataHidden, MetadataShown, MetadataShown}, MetadataHidden},
	}

	for name, tc := range cases {
		if got := Strictest(tc.in...); got != tc.want {
			t.Errorf("%s: Strictest = %q, want %q", name, got, tc.want)
		}
	}
}
