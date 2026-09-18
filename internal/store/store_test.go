// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"imvault/internal/db"
	"imvault/internal/models"
)

func newTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()

	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	return New(database), ctx
}

func mustUser(t *testing.T, s *Store, ctx context.Context, name string) *models.User {
	t.Helper()
	u, err := s.CreateUser(ctx, NewUser{Username: name, Email: name + "@example.com", PasswordHash: "hash"})
	if err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	return u
}

func mustFile(t *testing.T, s *Store, ctx context.Context, id string, owner *int64, public bool, expires *time.Time) *models.File {
	t.Helper()
	f := &models.File{
		ID:           id,
		UserID:       owner,
		OriginalName: id + ".jpg",
		Ext:          "jpg",
		Mime:         "image/jpeg",
		Size:         1024,
		Width:        100,
		Height:       80,
		SHA256:       "deadbeef" + id,
		ObjectKey:    "orig/" + id + ".jpg",
		ThumbKey:     "thumb/" + id + ".jpg",
		PreviewKey:   "preview/" + id + ".jpg",
		IsPublic:     public,
		CreatedAt:    time.Now().UTC(),
		ExpiresAt:    expires,
	}

	// Content is recorded before the file that refers to it, as the upload path
	// does: the trigger that counts references has nothing to update otherwise.
	if err := s.EnsureBlob(ctx, f.SHA256, f.Size, f.ObjectKey, f.ThumbKey, f.PreviewKey); err != nil {
		t.Fatalf("ensure blob for %s: %v", id, err)
	}
	if err := s.CreateFile(ctx, f); err != nil {
		t.Fatalf("create file %s: %v", id, err)
	}
	return f
}

func TestUsersAndSessions(t *testing.T) {
	s, ctx := newTestStore(t)

	u, err := s.CreateUser(ctx, NewUser{Username: "marcus", Email: "marcus@example.com", PasswordHash: "hash", IsAdmin: true})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if !u.IsAdmin {
		t.Error("first user should be admin")
	}

	if _, err := s.CreateUser(ctx, NewUser{Username: "MARCUS", PasswordHash: "hash"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate username error = %v, want ErrConflict", err)
	}

	// Usernames are case-insensitive.
	got, err := s.UserByUsername(ctx, "MaRcUs")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("lookup returned user %d, want %d", got.ID, u.ID)
	}

	if _, err := s.UserByUsername(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing user error = %v, want ErrNotFound", err)
	}

	if err := s.CreateSession(ctx, "tokenhash", u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	authed, err := s.UserBySession(ctx, "tokenhash", time.Now())
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if authed.ID != u.ID {
		t.Errorf("session resolved to %d, want %d", authed.ID, u.ID)
	}

	// A session past its expiry must not resolve.
	if _, err := s.UserBySession(ctx, "tokenhash", time.Now().Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session error = %v, want ErrNotFound", err)
	}

	if err := s.CreateSession(ctx, "old", u.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("create stale session: %v", err)
	}
	pruned, err := s.DeleteExpiredSessions(ctx, time.Now())
	if err != nil {
		t.Fatalf("prune sessions: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned %d sessions, want 1", pruned)
	}
}

func TestFileListingRespectsVisibilityAndSearch(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")

	mustFile(t, s, ctx, "alicepublic", &alice.ID, true, nil)
	mustFile(t, s, ctx, "aliceprivate", &alice.ID, false, nil)
	mustFile(t, s, ctx, "bobpublic", &bob.ID, true, nil)
	mustFile(t, s, ctx, "anonymous", nil, true, nil)

	// Owner listing sees only their own uploads, public or not.
	files, err := s.ListFiles(ctx, FileQuery{OwnerID: &alice.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list by owner: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("alice has %d files, want 2", len(files))
	}

	// Public listing excludes private files.
	files, err = s.ListFiles(ctx, FileQuery{PublicOnly: true, Limit: 10})
	if err != nil {
		t.Fatalf("list public: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("public listing has %d files, want 3", len(files))
	}

	// VisibleTo returns public files plus the viewer's own private ones.
	files, err = s.ListFiles(ctx, FileQuery{VisibleTo: &alice.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list visible: %v", err)
	}
	if len(files) != 4 {
		t.Errorf("alice-visible listing has %d files, want 4", len(files))
	}

	// Search matches the id.
	files, err = s.ListFiles(ctx, FileQuery{Search: "bobpub", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(files) != 1 || files[0].ID != "bobpublic" {
		t.Errorf("search returned %d files, want just bobpublic", len(files))
	}

	// LIKE wildcards in user input are treated literally.
	files, err = s.ListFiles(ctx, FileQuery{Search: "%", Limit: 10})
	if err != nil {
		t.Fatalf("wildcard search: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("literal %% search returned %d files, want 0", len(files))
	}
}

func TestExpiredFilesAndDeletion(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)

	mustFile(t, s, ctx, "gone", nil, true, &past)
	mustFile(t, s, ctx, "later", nil, true, &future)
	mustFile(t, s, ctx, "forever", &alice.ID, false, nil)

	expired, err := s.ExpiredFiles(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("expired files: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != "gone" {
		t.Fatalf("expired = %+v, want just 'gone'", expired)
	}

	if err := s.DeleteFile(ctx, "gone"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.FileByID(ctx, "gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete, error = %v, want ErrNotFound", err)
	}
	if _, err := s.FileByID(ctx, "later"); err != nil {
		t.Errorf("unrelated file was removed: %v", err)
	}
}

func TestTags(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	mustFile(t, s, ctx, "one", &alice.ID, false, nil)
	mustFile(t, s, ctx, "two", &alice.ID, false, nil)

	// Counts are a property of a viewer-scoped listing, not of the tag itself.
	countOf := func(name string) int {
		t.Helper()
		tags, err := s.ListTags(ctx, &alice.ID, 50)
		if err != nil {
			t.Fatalf("list tags: %v", err)
		}
		for _, candidate := range tags {
			if candidate.Name == name {
				return candidate.Count
			}
		}
		return 0
	}

	tag, err := s.AddTag(ctx, "one", &alice.ID, "Holiday Snaps")
	if err != nil {
		t.Fatalf("add tag: %v", err)
	}
	if tag.Slug != "holiday-snaps" {
		t.Errorf("slug = %q, want holiday-snaps", tag.Slug)
	}
	if got := countOf("Holiday Snaps"); got != 1 {
		t.Errorf("count = %d, want 1", got)
	}

	// Adding the same tag again is a no-op, not an error or a duplicate.
	if _, err := s.AddTag(ctx, "one", &alice.ID, "holiday snaps"); err != nil {
		t.Fatalf("re-add tag: %v", err)
	}
	tags, err := s.TagsForFile(ctx, "one")
	if err != nil {
		t.Fatalf("tags for file: %v", err)
	}
	if len(tags) != 1 {
		t.Errorf("file has %d tags, want 1", len(tags))
	}

	// A second file sharing the tag bumps the count.
	if _, err := s.AddTag(ctx, "two", &alice.ID, "Holiday Snaps"); err != nil {
		t.Fatalf("tag second file: %v", err)
	}
	if tag, err = s.TagBySlugInNamespace(ctx, &alice.ID, "holiday-snaps"); err != nil {
		t.Fatalf("tag by slug: %v", err)
	}
	if got := countOf("Holiday Snaps"); got != 2 {
		t.Errorf("count = %d, want 2", got)
	}

	// Removing from one file keeps the tag; removing from the last prunes it.
	if err := s.RemoveTag(ctx, "one", tag.ID); err != nil {
		t.Fatalf("remove tag: %v", err)
	}
	if _, err := s.TagBySlugInNamespace(ctx, &alice.ID, "holiday-snaps"); err != nil {
		t.Fatalf("tag should survive: %v", err)
	}
	if err := s.RemoveTag(ctx, "two", tag.ID); err != nil {
		t.Fatalf("remove last tag: %v", err)
	}
	if _, err := s.TagBySlugInNamespace(ctx, &alice.ID, "holiday-snaps"); !errors.Is(err, ErrNotFound) {
		t.Errorf("orphaned tag error = %v, want ErrNotFound", err)
	}
}

func TestAlbums(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")

	a, err := s.CreateAlbum(ctx, alice.ID, "Summer 2026", "Warm ones", true)
	if err != nil {
		t.Fatalf("create album: %v", err)
	}
	if a.Slug != "summer-2026" {
		t.Errorf("slug = %q, want summer-2026", a.Slug)
	}

	// A colliding title gets a distinct slug rather than failing.
	b, err := s.CreateAlbum(ctx, bob.ID, "Summer 2026", "", false)
	if err != nil {
		t.Fatalf("create colliding album: %v", err)
	}
	if b.Slug == a.Slug {
		t.Errorf("slug %q was reused", b.Slug)
	}

	mustFile(t, s, ctx, "pic1", &alice.ID, false, nil)
	mustFile(t, s, ctx, "pic2", &alice.ID, false, nil)

	if err := s.AddFileToAlbum(ctx, a.ID, "pic1"); err != nil {
		t.Fatalf("add file: %v", err)
	}
	// Idempotent.
	if err := s.AddFileToAlbum(ctx, a.ID, "pic1"); err != nil {
		t.Fatalf("re-add file: %v", err)
	}

	members, err := s.AlbumMembership(ctx, a.ID)
	if err != nil {
		t.Fatalf("membership: %v", err)
	}
	if len(members) != 1 || !members["pic1"] {
		t.Errorf("membership = %v, want just pic1", members)
	}

	files, err := s.ListFiles(ctx, FileQuery{AlbumID: &a.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list album files: %v", err)
	}
	if len(files) != 1 || files[0].ID != "pic1" {
		t.Errorf("album listing = %+v, want just pic1", files)
	}

	got, err := s.AlbumBySlug(ctx, a.Slug)
	if err != nil {
		t.Fatalf("album by slug: %v", err)
	}
	if got.FileCount != 1 {
		t.Errorf("file count = %d, want 1", got.FileCount)
	}

	// Deleting an album must not delete its files.
	if err := s.DeleteAlbum(ctx, a.ID); err != nil {
		t.Fatalf("delete album: %v", err)
	}
	if _, err := s.FileByID(ctx, "pic1"); err != nil {
		t.Errorf("file was removed with its album: %v", err)
	}
	if _, err := s.FileByID(ctx, "pic2"); err != nil {
		t.Errorf("unrelated file vanished: %v", err)
	}
}

func TestTagByRefInUser(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	mustFile(t, s, ctx, "pic", &alice.ID, false, nil)

	tag, err := s.AddTag(ctx, "pic", &alice.ID, "Holiday Snaps")
	if err != nil {
		t.Fatalf("add tag: %v", err)
	}
	if tag.Username != "alice" {
		t.Errorf("tag owner = %q, want alice", tag.Username)
	}

	refs := map[string]string{
		"by id":   strconv.FormatInt(tag.ID, 10),
		"by slug": "holiday-snaps",
		"by name": "Holiday Snaps",
	}
	for label, ref := range refs {
		got, err := s.TagByRefInNamespace(ctx, &alice.ID, ref)
		if err != nil {
			t.Errorf("TagByRefInUser(%s, %q): %v", label, ref, err)
			continue
		}
		if got.ID != tag.ID {
			t.Errorf("TagByRefInUser(%s, %q) = tag %d, want %d", label, ref, got.ID, tag.ID)
		}
	}

	// The name lookup is case-insensitive, matching the uniqueness rule.
	if _, err := s.TagByRefInNamespace(ctx, &alice.ID, "hOlIdAy sNaPs"); err != nil {
		t.Errorf("TagByRefInUser is case sensitive: %v", err)
	}

	for _, ref := range []string{"", "   ", "nosuchtag", "9999"} {
		if _, err := s.TagByRefInNamespace(ctx, &alice.ID, ref); !errors.Is(err, ErrNotFound) {
			t.Errorf("TagByRefInUser(%q) error = %v, want ErrNotFound", ref, err)
		}
	}
}

func TestTagsAreScopedPerAccount(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")

	mustFile(t, s, ctx, "alicepic", &alice.ID, false, nil)
	mustFile(t, s, ctx, "bobpic", &bob.ID, false, nil)

	// The same name in two namespaces yields two independent rows.
	aliceTag, err := s.AddTag(ctx, "alicepic", &alice.ID, "beach")
	if err != nil {
		t.Fatalf("alice tag: %v", err)
	}
	bobTag, err := s.AddTag(ctx, "bobpic", &bob.ID, "beach")
	if err != nil {
		t.Fatalf("bob tag: %v", err)
	}
	if aliceTag.ID == bobTag.ID {
		t.Fatal("two accounts shared a tag row")
	}
	if !aliceTag.OwnedBy(alice.ID) || !bobTag.OwnedBy(bob.ID) {
		t.Errorf("owners = %v/%v, want %d/%d", aliceTag.UserID, bobTag.UserID, alice.ID, bob.ID)
	}

	// Each namespace resolves its own tag.
	got, err := s.TagByRefInNamespace(ctx, &bob.ID, "beach")
	if err != nil {
		t.Fatalf("bob lookup: %v", err)
	}
	if got.ID != bobTag.ID {
		t.Errorf("bob resolved tag %d, want %d", got.ID, bobTag.ID)
	}

	// Removing one leaves the other alone.
	if err := s.RemoveTag(ctx, "alicepic", aliceTag.ID); err != nil {
		t.Fatalf("remove alice tag: %v", err)
	}
	if _, err := s.TagByRefInNamespace(ctx, &bob.ID, "beach"); err != nil {
		t.Errorf("bob's tag was affected by alice's removal: %v", err)
	}
}

func TestDeletingATagOwnerRemovesTheirTags(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	mustFile(t, s, ctx, "pic", &alice.ID, false, nil)
	if _, err := s.AddTag(ctx, "pic", &alice.ID, "beach"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DB().ExecContext(ctx, `DELETE FROM users WHERE id = ?`, alice.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, err := s.TagByRefInNamespace(ctx, &alice.ID, "beach"); !errors.Is(err, ErrNotFound) {
		t.Errorf("tag outlived its owner: %v", err)
	}
}

func TestFileQueryFiltersByKindAndPrivacy(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")

	mustFileKind(t, s, ctx, "still", &alice.ID, true, models.KindImage)
	mustFileKind(t, s, ctx, "wiggle", &alice.ID, true, models.KindAnimated)
	mustFileKind(t, s, ctx, "clip", &alice.ID, false, models.KindVideo)

	count := func(q FileQuery) int {
		t.Helper()
		n, err := s.CountFiles(ctx, q)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	if got := count(FileQuery{OwnerID: &alice.ID}); got != 3 {
		t.Errorf("unfiltered = %d, want 3", got)
	}

	for kind, want := range map[models.Kind]int{
		models.KindImage:    1,
		models.KindAnimated: 1,
		models.KindVideo:    1,
	} {
		k := kind
		if got := count(FileQuery{OwnerID: &alice.ID, Kind: &k}); got != want {
			t.Errorf("kind %s = %d, want %d", kind, got, want)
		}
	}

	if got := count(FileQuery{OwnerID: &alice.ID, PublicOnly: true}); got != 2 {
		t.Errorf("public = %d, want 2", got)
	}
	if got := count(FileQuery{OwnerID: &alice.ID, PrivateOnly: true}); got != 1 {
		t.Errorf("private = %d, want 1", got)
	}

	// Filters compose.
	kind := models.KindVideo
	if got := count(FileQuery{OwnerID: &alice.ID, Kind: &kind, PrivateOnly: true}); got != 1 {
		t.Errorf("private video = %d, want 1", got)
	}
	if got := count(FileQuery{OwnerID: &alice.ID, Kind: &kind, PublicOnly: true}); got != 0 {
		t.Errorf("public video = %d, want 0", got)
	}

	// A legacy row with no kind recorded reads back as a plain image.
	files, err := s.ListFiles(ctx, FileQuery{OwnerID: &alice.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, f := range files {
		if f.Kind == "" {
			t.Errorf("file %s has an empty kind", f.ID)
		}
	}
}

// mustFileKind inserts a file of a specific media kind.
func mustFileKind(t *testing.T, s *Store, ctx context.Context, id string, owner *int64, public bool, kind models.Kind) *models.File {
	t.Helper()

	f := mustFile(t, s, ctx, id, owner, public, nil)
	if _, err := s.DB().ExecContext(ctx, `UPDATE files SET kind = ? WHERE id = ?`, string(kind), id); err != nil {
		t.Fatalf("set kind: %v", err)
	}
	f.Kind = kind
	return f
}

func TestListTagsFollowsFileVisibility(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")

	mustFile(t, s, ctx, "alicepublic", &alice.ID, true, nil)
	mustFile(t, s, ctx, "aliceprivate", &alice.ID, false, nil)
	mustFile(t, s, ctx, "bobprivate", &bob.ID, false, nil)

	for _, link := range []struct {
		file  string
		tag   string
		owner int64
	}{
		{"alicepublic", "shared", alice.ID},
		{"aliceprivate", "alice-secret", alice.ID},
		{"bobprivate", "bob-secret", bob.ID},
	} {
		if _, err := s.AddTag(ctx, link.file, &link.owner, link.tag); err != nil {
			t.Fatalf("tag %s: %v", link.file, err)
		}
	}

	names := func(tags []models.Tag) []string {
		out := make([]string, 0, len(tags))
		for _, tag := range tags {
			out = append(out, tag.Name)
		}
		sort.Strings(out)
		return out
	}
	sameSet := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	// An anonymous visitor sees only tags on public files.
	anon, err := s.ListTags(ctx, nil, 50)
	if err != nil {
		t.Fatalf("list tags anonymously: %v", err)
	}
	if got := names(anon); !sameSet(got, []string{"shared"}) {
		t.Errorf("anonymous tag index = %v, want [shared]", got)
	}

	// Alice sees tags on public files plus her own private ones, but never
	// Bob's.
	aliceTags, err := s.ListTags(ctx, &alice.ID, 50)
	if err != nil {
		t.Fatalf("list tags for alice: %v", err)
	}
	if got := names(aliceTags); !sameSet(got, []string{"alice-secret", "shared"}) {
		t.Errorf("alice tag index = %v, want [alice-secret shared]", got)
	}

	// Bob sees her public tag too, since the file carrying it is public.
	bobTags, err := s.ListTags(ctx, &bob.ID, 50)
	if err != nil {
		t.Fatalf("list tags for bob: %v", err)
	}
	if got := names(bobTags); !sameSet(got, []string{"bob-secret", "shared"}) {
		t.Errorf("bob tag index = %v, want [bob-secret shared]", got)
	}
}

func TestListTagsCountsOnlyVisibleFiles(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")

	// The same tag on one public and two private files: an anonymous visitor
	// must count one, not three.
	mustFile(t, s, ctx, "pub", &alice.ID, true, nil)
	mustFile(t, s, ctx, "priv1", &alice.ID, false, nil)
	mustFile(t, s, ctx, "priv2", &alice.ID, false, nil)

	for _, id := range []string{"pub", "priv1", "priv2"} {
		if _, err := s.AddTag(ctx, id, &alice.ID, "grouped"); err != nil {
			t.Fatal(err)
		}
	}

	anon, err := s.ListTags(ctx, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(anon) != 1 {
		t.Fatalf("anonymous tag index = %d tags, want 1", len(anon))
	}
	if anon[0].Count != 1 {
		t.Errorf("anonymous count = %d, want 1 (only the public file)", anon[0].Count)
	}

	aliceTags, err := s.ListTags(ctx, &alice.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceTags) != 1 || aliceTags[0].Count != 3 {
		t.Errorf("alice sees %+v, want one tag with count 3", aliceTags)
	}
}

func TestTagCountsAreScopedToTheViewer(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")

	mustFile(t, s, ctx, "alicesprivate", &alice.ID, false, nil)
	mustFile(t, s, ctx, "bobspublic", &bob.ID, true, nil)

	aliceTag, err := s.AddTag(ctx, "alicesprivate", &alice.ID, "secret project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTag(ctx, "bobspublic", &bob.ID, "secret project"); err != nil {
		t.Fatal(err)
	}

	// Alice's count covers her file; bob's tag of the same name is a different
	// row and does not contribute.
	count, err := s.CountTagVisibleTo(ctx, aliceTag.ID, &alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("alice sees %d files for her tag, want 1", count)
	}

	// Nobody else can see anything under alice's tag.
	for _, viewer := range []*int64{nil, &bob.ID} {
		count, err := s.CountTagVisibleTo(ctx, aliceTag.ID, viewer)
		if err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("viewer %v sees %d files for alice's private tag, want 0", viewer, count)
		}
	}
}

func TestExpiredFilesLeaveListingsAndTagCounts(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	past := time.Now().Add(-time.Minute)

	mustFile(t, s, ctx, "live", &alice.ID, true, nil)
	mustFile(t, s, ctx, "gone", &alice.ID, true, &past)

	if _, err := s.AddTag(ctx, "live", &alice.ID, "keep"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTag(ctx, "gone", &alice.ID, "keep"); err != nil {
		t.Fatal(err)
	}

	// The expired file is gone from listings even before the reaper runs...
	files, err := s.ListFiles(ctx, FileQuery{PublicOnly: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].ID != "live" {
		t.Errorf("listing = %+v, want just the live file", files)
	}

	// ...and does not inflate the tag count.
	tags, err := s.ListTags(ctx, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].Count != 1 {
		t.Errorf("tags = %+v, want one tag with count 1", tags)
	}
}

func TestDeleteFileCascadesJoinRows(t *testing.T) {
	s, ctx := newTestStore(t)

	alice := mustUser(t, s, ctx, "alice")
	mustFile(t, s, ctx, "pic", &alice.ID, false, nil)

	album, err := s.CreateAlbum(ctx, alice.ID, "Trip", "", false)
	if err != nil {
		t.Fatalf("create album: %v", err)
	}
	if err := s.AddFileToAlbum(ctx, album.ID, "pic"); err != nil {
		t.Fatalf("add to album: %v", err)
	}
	tag, err := s.AddTag(ctx, "pic", &alice.ID, "sunset")
	if err != nil {
		t.Fatalf("add tag: %v", err)
	}

	if err := s.DeleteFile(ctx, "pic"); err != nil {
		t.Fatalf("delete file: %v", err)
	}

	members, err := s.AlbumMembership(ctx, album.ID)
	if err != nil {
		t.Fatalf("membership: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("album still references the deleted file: %v", members)
	}
	// The tag row survives but is now orphaned; PruneUnusedTags clears it.
	pruned, err := s.PruneUnusedTags(ctx)
	if err != nil {
		t.Fatalf("prune tags: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned %d tags, want 1", pruned)
	}
	if _, err := s.TagBySlugInNamespace(ctx, &alice.ID, "sunset"); !errors.Is(err, ErrNotFound) {
		t.Errorf("tag %d should be gone, got %v", tag.ID, err)
	}
}
