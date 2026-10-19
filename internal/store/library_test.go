// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"imvault/internal/metadata"
	"imvault/internal/models"
)

func TestTrashLifecyclePreservesReferencesAndRelations(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "alice")
	other := mustUser(t, s, ctx, "bob")
	f := mustFile(t, s, ctx, "photo", &owner.ID, models.VisibilityPublic, nil)
	album, err := s.CreateAlbum(ctx, owner.ID, AlbumInput{Title: "Holiday", Visibility: models.VisibilityPublic})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddFileToAlbum(ctx, album.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTag(ctx, f.ID, &owner.ID, "Summer"); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Truncate(time.Second)
	if err := s.TrashFile(ctx, f.ID, other.ID, at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign trash: %v", err)
	}
	if err := s.TrashFile(ctx, f.ID, owner.ID, at); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats(ctx)
	if err != nil || stats.PublicFiles != 0 || stats.TotalBytes != f.Size {
		t.Fatal("trash sharing or storage counts", stats, err)
	}
	if err := s.TrashFile(ctx, f.ID, owner.ID, at.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatal("retry extended retention", err)
	}
	if _, err := s.FileByID(ctx, f.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("live lookup exposed Trash", err)
	}
	for _, q := range []FileQuery{{OwnerID: &owner.ID}, {PublicOnly: true}, {VisibleTo: &other.ID}, {AlbumID: &album.ID}} {
		files, err := s.ListFiles(ctx, q)
		if err != nil || len(files) != 0 {
			t.Fatalf("listing exposed Trash: %+v %v", q, err)
		}
		n, err := s.CountFiles(ctx, q)
		if err != nil || n != 0 {
			t.Fatal("count exposed Trash", n, err)
		}
	}
	a, err := s.AlbumByID(ctx, album.ID)
	if err != nil || a.FileCount != 0 {
		t.Fatal("album count exposed Trash", a, err)
	}
	blob, err := s.BlobBySHA(ctx, f.SHA256)
	if err != nil || blob.Refcount != 1 {
		t.Fatal("Trash released content", blob, err)
	}
	files, err := s.ListTrash(ctx, owner.ID, 10, 0)
	if err != nil || len(files) != 1 || !files[0].TrashedAt.Equal(at) {
		t.Fatal("Trash listing", files, err)
	}
	foreign, err := s.ListTrash(ctx, other.ID, 10, 0)
	if err != nil || len(foreign) != 0 {
		t.Fatal("foreign trash", foreign, err)
	}
	if err := s.RestoreTrashedFile(ctx, f.ID, other.ID, at); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign restore", err)
	}
	if err := s.RestoreTrashedFile(ctx, f.ID, owner.ID, at.Add(TrashRetention-time.Second)); err != nil {
		t.Fatal(err)
	}
	restored, err := s.FileByID(ctx, f.ID)
	if err != nil || restored.Visibility != f.Visibility || len(restored.Tags) != 1 {
		t.Fatal("restore lost properties", restored, err)
	}
	a, err = s.AlbumByID(ctx, album.ID)
	if err != nil || a.FileCount != 1 {
		t.Fatal("restore lost membership", a, err)
	}
	if _, err := s.PurgeTrashedFile(ctx, f.ID, owner.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale purge deleted live file", err)
	}
	if deleted, err := s.DeleteExpiredFile(ctx, f.ID, at.Add(TrashRetention)); err != nil || deleted {
		t.Fatal("stale cleanup deleted live file", err)
	}
}

func TestTrashDeadlinePurgeAndAuthorization(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "alice")
	other := mustUser(t, s, ctx, "bob")
	at := time.Now().Truncate(time.Second)
	for _, id := range []string{"boundary", "expired", "purge"} {
		mustFile(t, s, ctx, id, &owner.ID, models.VisibilityPrivate, nil)
		if err := s.TrashFile(ctx, id, owner.ID, at); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"boundary", "expired"} {
		end := at.Add(TrashRetention)
		if id == "expired" {
			end = end.Add(time.Second)
		}
		if err := s.RestoreTrashedFile(ctx, id, owner.ID, end); !errors.Is(err, ErrNotFound) {
			t.Fatal("restored after deadline", err)
		}
	}
	if _, err := s.PurgeTrashedFile(ctx, "purge", other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign purge", err)
	}
	f, err := s.PurgeTrashedFile(ctx, "purge", owner.ID)
	if err != nil || f.ID != "purge" {
		t.Fatal("purge", f, err)
	}
	if _, err := s.PurgeTrashedFile(ctx, "purge", owner.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("repeated purge", err)
	}
	batch, err := s.ExpiredFiles(ctx, at.Add(TrashRetention-time.Second), 10)
	if err != nil || len(batch) != 0 {
		t.Fatal("early cleanup", batch, err)
	}
	batch, err = s.ExpiredFiles(ctx, at.Add(TrashRetention), 10)
	if err != nil || len(batch) != 2 {
		t.Fatal("deadline cleanup", batch, err)
	}
	for _, f := range batch {
		if deleted, err := s.DeleteExpiredFile(ctx, f.ID, at.Add(TrashRetention)); err != nil || !deleted {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"boundary", "expired", "purge"} {
		if _, err := s.TrashedFile(ctx, id, owner.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("purge retained row", id, err)
		}
	}
}

func TestTrashRestorePurgeRaceIsAtomic(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "alice")
	for i := 0; i < 20; i++ {
		id := fmt.Sprint(i)
		mustFile(t, s, ctx, id, &owner.ID, models.VisibilityPrivate, nil)
		if err := s.TrashFile(ctx, id, owner.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		var restore, purge error
		go func() { defer wg.Done(); restore = s.RestoreTrashedFile(ctx, id, owner.ID, time.Now()) }()
		go func() { defer wg.Done(); _, purge = s.PurgeTrashedFile(ctx, id, owner.ID) }()
		wg.Wait()
		if (restore == nil) == (purge == nil) {
			t.Fatalf("exactly one operation should succeed: restore=%v purge=%v", restore, purge)
		}
		_, err := s.FileByID(ctx, id)
		if (restore == nil) != (err == nil) {
			t.Fatal("race lost a restored file", err)
		}
	}
}

func TestBulkLibraryAuthorizationAndRollback(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "alice")
	other := mustUser(t, s, ctx, "bob")
	for _, id := range []string{"first", "second", "trash"} {
		mustFile(t, s, ctx, id, &owner.ID, models.VisibilityPrivate, nil)
	}
	mustFile(t, s, ctx, "foreign", &other.ID, models.VisibilityPublic, nil)
	if err := s.TrashFile(ctx, "trash", owner.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	album, err := s.CreateAlbum(ctx, owner.ID, AlbumInput{Title: "Album"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"foreign", "trash", "missing", "' OR 1=1 --"} {
		for _, ids := range [][]string{{"first", bad}, {bad, "first"}} {
			if n, err := s.SetFilesVisibility(ctx, owner.ID, ids, models.VisibilityPublic); n != 0 || !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid sharing batch", n, err)
			}
			if n, err := s.AddFilesToAlbum(ctx, owner.ID, ids, album.ID); n != 0 || !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid album batch", n, err)
			}
		}
	}
	if n, err := s.SetFilesVisibility(ctx, owner.ID, []string{"first"}, "oops"); n != 0 || !errors.Is(err, ErrInvalidTags) {
		t.Fatal("invalid visibility", n, err)
	}
	for _, ids := range [][]string{nil, {""}, {"a\x00b"}, make([]string, MaxBulkTagFiles+1)} {
		if _, err := s.SetFilesVisibility(ctx, owner.ID, ids, models.VisibilityPublic); !errors.Is(err, ErrInvalidTags) {
			t.Fatal("invalid selection", err)
		}
	}
	for _, trigger := range []struct {
		ddl string
		run func() (int, error)
	}{
		{`CREATE TRIGGER fail_library BEFORE UPDATE OF visibility ON files WHEN OLD.id='second' BEGIN SELECT RAISE(ABORT,'injected'); END`, func() (int, error) {
			return s.SetFilesVisibility(ctx, owner.ID, []string{"first", "second"}, models.VisibilityPublic)
		}},
		{`CREATE TRIGGER fail_library BEFORE INSERT ON album_files WHEN NEW.file_id='second' BEGIN SELECT RAISE(ABORT,'injected'); END`, func() (int, error) { return s.AddFilesToAlbum(ctx, owner.ID, []string{"first", "second"}, album.ID) }},
	} {
		if _, err := s.db.Exec(trigger.ddl); err != nil {
			t.Fatal(err)
		}
		if n, err := trigger.run(); n != 0 || err == nil {
			t.Fatal("injected failure accepted", n, err)
		}
		if _, err := s.db.Exec(`DROP TRIGGER fail_library`); err != nil {
			t.Fatal(err)
		}
	}
	f, err := s.FileByID(ctx, "first")
	if err != nil || f.Visibility != models.VisibilityPrivate {
		t.Fatal("failed batch changed visibility", f, err)
	}
	a, err := s.AlbumByID(ctx, album.ID)
	if err != nil || a.FileCount != 0 {
		t.Fatal("failed batch changed album", a, err)
	}
}

func TestBulkAlbumContributionPolicyAndSubsetProperty(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "alice")
	other := mustUser(t, s, ctx, "bob")
	for i := 0; i < 4; i++ {
		mustFile(t, s, ctx, fmt.Sprint(i), &owner.ID, models.VisibilityPrivate, nil)
	}
	shared, err := s.CreateAlbum(ctx, other.ID, AlbumInput{Title: "Shared", Visibility: models.VisibilityMembers, Access: models.AlbumAccessMembers})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := s.CreateAlbum(ctx, other.ID, AlbumInput{Title: "Closed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFilesToAlbum(ctx, owner.ID, []string{"0"}, closed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("closed album accepted contribution", err)
	}
	for mask := 1; mask < 16; mask++ {
		var ids []string
		for i := 0; i < 4; i++ {
			if mask&(1<<i) != 0 {
				ids = append(ids, fmt.Sprint(i))
			}
		}
		for retry := 0; retry < 2; retry++ {
			if n, err := s.SetFilesVisibility(ctx, owner.ID, append(ids, ids...), models.VisibilityMembers); err != nil || n != len(ids) {
				t.Fatal(n, err)
			}
			if n, err := s.AddFilesToAlbum(ctx, owner.ID, append(ids, ids...), shared.ID); err != nil || n != len(ids) {
				t.Fatal(n, err)
			}
		}
		membership, err := s.AlbumMembership(ctx, shared.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			id := fmt.Sprint(i)
			f, err := s.FileByID(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			want := mask&(1<<i) != 0
			if (f.Visibility == models.VisibilityMembers) != want || membership[id] != want {
				t.Fatal("subset modified unselected file", mask, id)
			}
			if err := s.SetFileVisibility(ctx, id, models.VisibilityPrivate); err != nil {
				t.Fatal(err)
			}
			if err := s.RemoveFileFromAlbum(ctx, shared.ID, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.UpdateAlbum(ctx, shared.ID, AlbumInput{Title: "Now closed", Visibility: models.VisibilityMembers}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFilesToAlbum(ctx, owner.ID, []string{"0"}, shared.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale album policy accepted", err)
	}
}

func TestCaptureDatesSortFilterRefreshAndScope(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "alice")
	other := mustUser(t, s, ctx, "bob")
	dates := map[string]string{"old": "31 December 2019 at 23:59", "new": "1 January 2024 at 00:01", "tie-a": "1 January 2024 at 00:01", "tie-b": "1 January 2024 at 00:01", "unknown": "", "invalid": "31 February 2024 at 00:00"}
	for id, taken := range dates {
		f := mustFile(t, s, ctx, id, &owner.ID, models.VisibilityPublic, nil)
		if err := s.SetBlobDetails(ctx, f.SHA256, (&metadata.Details{Taken: taken}).Encode()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE files SET created_at=100 WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(q FileQuery) []string {
		t.Helper()
		files, err := s.ListFiles(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		n, err := s.CountFiles(ctx, q)
		if err != nil || n != len(files) {
			t.Fatal("count disagrees", n, err)
		}
		out := []string{}
		for _, f := range files {
			out = append(out, f.ID)
		}
		return out
	}
	if got := ids(FileQuery{OwnerID: &owner.ID, SortTaken: true}); !reflect.DeepEqual(got, []string{"tie-b", "tie-a", "new", "old", "unknown", "invalid"}) {
		t.Fatal("date order", got)
	}
	for _, tc := range []struct {
		q    FileQuery
		want []string
	}{
		{FileQuery{OwnerID: &owner.ID, TakenPrefix: "2024-01-", SortTaken: true}, []string{"tie-b", "tie-a", "new"}},
		{FileQuery{OwnerID: &owner.ID, TakenPrefix: "2019-"}, []string{"old"}},
		{FileQuery{OwnerID: &owner.ID, UnknownTaken: true}, []string{"unknown", "invalid"}},
		{FileQuery{OwnerID: &other.ID, TakenPrefix: "2024-"}, []string{}},
	} {
		if got := ids(tc.q); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("filter %+v: %v want %v", tc.q, got, tc.want)
		}
	}
	years, err := s.CaptureYears(ctx, owner.ID)
	if err != nil || !reflect.DeepEqual(years, []string{"2024", "2019"}) {
		t.Fatal("years", years, err)
	}
	// An ownerless query cannot infer hidden dates through sorting or filtering.
	if got := ids(FileQuery{PublicOnly: true, TakenPrefix: "does-not-exist", SortTaken: true, UnknownTaken: true}); len(got) != 6 {
		t.Fatal("public filter exposed capture metadata", got)
	}
	if err := s.SetBlobDetails(ctx, "deadbeefold", (&metadata.Details{Taken: "5 June 2025 at 12:00"}).Encode()); err != nil {
		t.Fatal(err)
	}
	if got := ids(FileQuery{OwnerID: &owner.ID, SortTaken: true}); got[0] != "old" {
		t.Fatal("metadata refresh did not update index", got)
	}
	if _, err := s.SetFilesVisibility(ctx, owner.ID, []string{strings.Repeat("x", 129)}, models.VisibilityPublic); !errors.Is(err, ErrInvalidTags) {
		t.Fatal(err)
	}
}
