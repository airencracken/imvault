// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"imvault/internal/models"
)

func TestBulkTagsAddPreserveAndDeduplicate(t *testing.T) {
	s, ctx := newTestStore(t)
	u := mustUser(t, s, ctx, "alice")
	for _, id := range []string{"first", "second", "untouched"} {
		mustFile(t, s, ctx, id, &u.ID, models.VisibilityPrivate, nil)
	}
	if _, err := s.AddTag(ctx, "first", &u.ID, "Existing"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		count, err := s.AddTagsToFiles(ctx, u.ID, []string{"first", "second", "first"}, []string{" Holiday ", "holiday", "Family"})
		if err != nil || count != 2 {
			t.Fatalf("tagged = %d, %v", count, err)
		}
	}
	for id, want := range map[string]int{"first": 3, "second": 2, "untouched": 0} {
		tags, err := s.TagsForFile(ctx, id)
		if err != nil || len(tags) != want {
			t.Fatalf("%s: %v, %v", id, tags, err)
		}
		for _, tag := range tags {
			if !tag.OwnedBy(u.ID) {
				t.Fatal("wrong tag namespace")
			}
		}
	}
}

func TestBulkTagsAuthorizationIsAtomic(t *testing.T) {
	s, ctx := newTestStore(t)
	u := mustUser(t, s, ctx, "alice")
	other := mustUser(t, s, ctx, "bob")
	mustFile(t, s, ctx, "own", &u.ID, models.VisibilityPrivate, nil)
	mustFile(t, s, ctx, "other", &other.ID, models.VisibilityPublic, nil)
	mustFile(t, s, ctx, "anonymous", nil, models.VisibilityPublic, nil)
	expired := time.Now().Add(-time.Hour)
	mustFile(t, s, ctx, "expired", &u.ID, models.VisibilityPrivate, &expired)
	for _, id := range []string{"other", "anonymous", "expired", "missing", "' OR 1=1 --"} {
		for _, files := range [][]string{{"own", id}, {id, "own"}} {
			if n, err := s.AddTagsToFiles(ctx, u.ID, files, []string{"new"}); n != 0 || !errors.Is(err, ErrNotFound) {
				t.Fatalf("%v: %d, %v", files, n, err)
			}
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tags`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected batch created tags: %d, %v", n, err)
	}
}

func TestBulkTagsRollbackAfterWriteFailure(t *testing.T) {
	s, ctx := newTestStore(t)
	u := mustUser(t, s, ctx, "alice")
	for _, id := range []string{"first", "second"} {
		mustFile(t, s, ctx, id, &u.ID, models.VisibilityPrivate, nil)
	}
	if _, err := s.AddTag(ctx, "first", &u.ID, "existing"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_bulk BEFORE INSERT ON file_tags WHEN NEW.file_id = 'second' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.AddTagsToFiles(ctx, u.ID, []string{"first", "second"}, []string{"new"}); err == nil || n != 0 {
		t.Fatalf("failed batch = %d, %v", n, err)
	}
	tags, err := s.TagsForFile(ctx, "first")
	if err != nil || len(tags) != 1 || tags[0].Name != "existing" {
		t.Fatalf("rollback changed existing tags: %v, %v", tags, err)
	}
	if _, err := s.TagBySlugInNamespace(ctx, &u.ID, "new"); !errors.Is(err, ErrNotFound) {
		t.Fatal("rollback left an orphan tag")
	}
}

func TestBulkTagInputValidation(t *testing.T) {
	for _, values := range [][]string{nil, {""}, {"  "}, {"a\x00b"}, {"a\nb"}, {string([]byte{0xff})}, {strings.Repeat("é", 49)}, make([]string, MaxBulkTags+1)} {
		if _, err := bulkTagValues(values, MaxBulkTags, maxTagNameLen, "tags"); !errors.Is(err, ErrInvalidTags) {
			t.Errorf("accepted %q", values)
		}
	}
	if _, err := bulkTagValues([]string{strings.Repeat("é", 48), "<script>alert(1)</script>"}, MaxBulkTags, maxTagNameLen, "tags"); err != nil {
		t.Fatal(err)
	}
}

func TestBulkTagsSubsetAndRetryProperty(t *testing.T) {
	s, ctx := newTestStore(t)
	u := mustUser(t, s, ctx, "alice")
	for i := 0; i < 4; i++ {
		mustFile(t, s, ctx, fmt.Sprint(i), &u.ID, models.VisibilityPrivate, nil)
	}
	for mask := 1; mask < 16; mask++ {
		var files []string
		for i := 0; i < 4; i++ {
			if mask&(1<<i) != 0 {
				files = append(files, fmt.Sprint(i))
			}
		}
		name := fmt.Sprintf("subset-%d", mask)
		for retry := 0; retry < 2; retry++ {
			if _, err := s.AddTagsToFiles(ctx, u.ID, append(files, files...), []string{name, name}); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 4; i++ {
			tags, err := s.TagsForFile(ctx, fmt.Sprint(i))
			if err != nil {
				t.Fatal(err)
			}
			matches := 0
			for _, tag := range tags {
				if tag.Name == name {
					matches++
				}
			}
			want := 0
			if mask&(1<<i) != 0 {
				want = 1
			}
			if matches != want {
				t.Fatalf("mask %d file %d: got %d tags, want %d", mask, i, matches, want)
			}
		}
	}
}
