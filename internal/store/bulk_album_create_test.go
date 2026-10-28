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

func TestCreateAlbumWithFilesSubsetAndPrivacy(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "alice")
	for i := 0; i < 4; i++ {
		mustFile(t, s, ctx, fmt.Sprint(i), &owner.ID, models.VisibilityLevels()[i%3], nil)
	}
	for mask := 1; mask < 16; mask++ {
		var selection []string
		for i := 0; i < 4; i++ {
			if mask&(1<<i) != 0 {
				selection = append(selection, fmt.Sprint(i))
			}
		}
		title := fmt.Sprintf("Selection %d", mask)
		count, err := s.CreateAlbumWithFiles(ctx, owner.ID, append(selection, selection...), "  "+title+"  ")
		if err != nil || count != len(selection) {
			t.Fatalf("selection %d: count=%d, error=%v", mask, count, err)
		}
		album, err := s.AlbumBySlug(ctx, fmt.Sprintf("selection-%d", mask))
		if err != nil {
			t.Fatal(err)
		}
		if album.Title != title || album.UserID != owner.ID || album.Visibility != models.VisibilityPrivate || album.Access != models.AlbumAccessOwner || album.Metadata != models.MetadataInherit || album.Location != models.MetadataInherit || album.FileCount != len(selection) {
			t.Fatalf("new album defaults or membership changed: %+v", album)
		}
		members, err := s.AlbumMembership(ctx, album.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			id := fmt.Sprint(i)
			if members[id] != (mask&(1<<i) != 0) {
				t.Fatalf("selection %d changed unselected file %s", mask, id)
			}
			file, err := s.FileByID(ctx, id)
			if err != nil || file.Visibility != models.VisibilityLevels()[i%3] {
				t.Fatalf("album creation changed file visibility: %+v, %v", file, err)
			}
		}
	}
	// A reused title creates a separate album with a unique slug inside the transaction.
	if count, err := s.CreateAlbumWithFiles(ctx, owner.ID, []string{"0"}, "Selection 1"); err != nil || count != 1 {
		t.Fatal("duplicate album title", count, err)
	}
	albums, err := s.AlbumsByUser(ctx, owner.ID)
	if err != nil || len(albums) != 16 {
		t.Fatal("title collision lost an album", len(albums), err)
	}
}

func TestCreateAlbumWithFilesRejectsInvalidInputAndRollsBack(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "alice")
	other := mustUser(t, s, ctx, "bob")
	for _, id := range []string{"first", "second", "trash"} {
		mustFile(t, s, ctx, id, &owner.ID, models.VisibilityPublic, nil)
	}
	mustFile(t, s, ctx, "foreign", &other.ID, models.VisibilityPublic, nil)
	past := time.Now().Add(-time.Hour)
	mustFile(t, s, ctx, "expired", &owner.ID, models.VisibilityPrivate, &past)
	if err := s.TrashFile(ctx, "trash", owner.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"foreign", "expired", "trash", "missing", "' OR 1=1 --"} {
		for _, selection := range [][]string{{"first", bad}, {bad, "first"}} {
			if count, err := s.CreateAlbumWithFiles(ctx, owner.ID, selection, "Rejected album"); count != 0 || !errors.Is(err, ErrNotFound) {
				t.Fatal("unauthorized batch", selection, count, err)
			}
		}
	}
	for _, selection := range [][]string{nil, {""}, {"nul\x00id"}, make([]string, MaxBulkTagFiles+1)} {
		if count, err := s.CreateAlbumWithFiles(ctx, owner.ID, selection, "Rejected album"); count != 0 || !errors.Is(err, ErrInvalidTags) {
			t.Fatal("invalid selection", count, err)
		}
	}
	for _, title := range []string{"", " \n\t ", "nul\x00title", "line\nbreak", "invalid\xff", strings.Repeat("界", 121)} {
		if count, err := s.CreateAlbumWithFiles(ctx, owner.ID, []string{"first"}, title); count != 0 || !errors.Is(err, ErrInvalidTags) {
			t.Fatal("invalid title accepted", count, err)
		}
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_new_album BEFORE INSERT ON album_files WHEN NEW.file_id='second' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if count, err := s.CreateAlbumWithFiles(ctx, owner.ID, []string{"first", "second"}, "Atomic album"); count != 0 || err == nil {
		t.Fatal("failed membership write succeeded", count, err)
	}
	for _, table := range []string{"albums", "album_files"} {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed creation left rows in %s: %d, %v", table, count, err)
		}
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_new_album`); err != nil {
		t.Fatal(err)
	}
	if count, err := s.CreateAlbumWithFiles(ctx, owner.ID, []string{"first", "second"}, strings.Repeat("界", 120)); err != nil || count != 2 {
		t.Fatal("valid Unicode title at limit", count, err)
	}
}
