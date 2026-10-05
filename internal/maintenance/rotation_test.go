// SPDX-License-Identifier: AGPL-3.0-or-later
package maintenance

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func privateBackups(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func rotationID(t *testing.T, f fixture) string {
	t.Helper()
	id, err := backupInstance(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func rotatedNames(t *testing.T, parent, id string) []string {
	t.Helper()
	names, err := scheduledSnapshots(parent, id)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	return names
}

func datedBackup(t *testing.T, f fixture, parent string, year int, marked bool) string {
	t.Helper()
	name := "imvault-" + time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format(scheduledTime)
	path := filepath.Join(parent, name)
	if err := Backup(t.Context(), f.cfg, f.store, f.objects, path, io.Discard); err != nil {
		t.Fatal(err)
	}
	if marked {
		if err := markScheduledBackup(path, rotationID(t, f)); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestScheduledRetentionCountsAndRestoresForEachPolicy(t *testing.T) {
	f := newFixture(t)
	id := rotationID(t, f)
	for keep := 1; keep <= 8; keep++ {
		parent := privateBackups(t)
		var made []string
		for i := 0; i < keep+2; i++ {
			path, err := ScheduledBackup(t.Context(), f.cfg, f.store, f.objects, parent, keep, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			made = append(made, filepath.Base(path))
			want := made[max(0, len(made)-keep):]
			got := rotatedNames(t, parent, id)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("keep=%d iteration=%d: got %v want %v", keep, i, got, want)
			}
		}
		for _, name := range rotatedNames(t, parent, id) {
			if err := Restore(t.Context(), filepath.Join(parent, name), filepath.Join(t.TempDir(), "restored"), io.Discard); err != nil {
				t.Fatalf("retained backup cannot restore: %v", err)
			}
		}
	}
}

func TestRotationLeavesManualOtherInstanceMalformedAndSymlinkBackups(t *testing.T) {
	f := newFixture(t)
	parent := privateBackups(t)
	manual := datedBackup(t, f, parent, 2010, false)
	unrelated := datedBackup(t, f, parent, 2011, true)
	if err := os.Rename(unrelated, filepath.Join(parent, "family-important")); err != nil {
		t.Fatal(err)
	}
	other := newFixture(t)
	otherPath, err := ScheduledBackup(t.Context(), other.cfg, other.store, other.objects, parent, 0, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	invalid := datedBackup(t, f, parent, 2012, true)
	if err := os.WriteFile(filepath.Join(invalid, "manifest.json"), []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	trailing := datedBackup(t, f, parent, 2013, true)
	marker, err := os.ReadFile(filepath.Join(trailing, scheduledMarker))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trailing, scheduledMarker), append(marker, []byte(" false")...), 0o600); err != nil {
		t.Fatal(err)
	}
	external := datedBackup(t, f, t.TempDir(), 2014, true)
	link := filepath.Join(parent, filepath.Base(external))
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := ScheduledBackup(t.Context(), f.cfg, f.store, f.objects, parent, 1, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if got := rotatedNames(t, parent, rotationID(t, f)); len(got) != 1 {
		t.Fatalf("retention count: %v", got)
	}
	for _, path := range []string{manual, filepath.Join(parent, "family-important"), otherPath, invalid, trailing, link, external} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("rotation touched unrelated backup %s: %v", path, err)
		}
	}
	if err := Restore(t.Context(), external, filepath.Join(t.TempDir(), "restored"), io.Discard); err != nil {
		t.Fatal("symlink target changed", err)
	}
}

func TestFailedScheduledBackupAndMissingCurrentCannotPrune(t *testing.T) {
	f := newFixture(t)
	parent := privateBackups(t)
	for i := 0; i < 3; i++ {
		if _, err := ScheduledBackup(t.Context(), f.cfg, f.store, f.objects, parent, 0, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	id := rotationID(t, f)
	before := strings.Join(rotatedNames(t, parent, id), ",")
	if err := pruneScheduledBackups(t.Context(), parent, "missing", id, 1, io.Discard); err == nil {
		t.Fatal("rotation accepted a missing current snapshot")
	}
	blob, err := f.store.BlobBySHA(t.Context(), f.file.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.objects.Delete(t.Context(), blob.ObjectKey); err != nil {
		t.Fatal(err)
	}
	if _, err := ScheduledBackup(t.Context(), f.cfg, f.store, f.objects, parent, 1, io.Discard); err == nil {
		t.Fatal("backup with missing original succeeded")
	}
	if after := strings.Join(rotatedNames(t, parent, id), ","); after != before {
		t.Fatalf("failed backup pruned snapshots: before=%s after=%s", before, after)
	}
	for _, name := range rotatedNames(t, parent, id) {
		if err := Restore(t.Context(), filepath.Join(parent, name), filepath.Join(t.TempDir(), "restored"), io.Discard); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRotationPreservesNewSnapshotAfterClockMovesBack(t *testing.T) {
	f := newFixture(t)
	parent := privateBackups(t)
	current := datedBackup(t, f, parent, 2020, true)
	old := datedBackup(t, f, parent, 2030, true)
	latest := datedBackup(t, f, parent, 2040, true)
	if err := pruneScheduledBackups(t.Context(), parent, filepath.Base(current), rotationID(t, f), 2, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("older previous snapshot retained", err)
	}
	for _, path := range []string{current, latest} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("current or newest previous snapshot lost", err)
		}
	}
}

func TestConcurrentScheduledBackupsSerializeRotation(t *testing.T) {
	f := newFixture(t)
	parent := privateBackups(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errors := make(chan error, 6)
	for i := 0; i < cap(errors); i++ {
		wg.Go(func() {
			_, err := ScheduledBackup(ctx, f.cfg, f.store, f.objects, parent, 2, io.Discard)
			errors <- err
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := rotatedNames(t, parent, rotationID(t, f)); len(got) != 2 {
		t.Fatalf("concurrent backups retained %d snapshots", len(got))
	}
}

func TestScheduledBackupWaitsForDestinationLock(t *testing.T) {
	f := newFixture(t)
	parent := privateBackups(t)
	lock, err := lockBackupDestination(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := ScheduledBackup(ctx, f.cfg, f.store, f.objects, parent, 1, io.Discard); err == nil {
		t.Fatal("scheduled backup bypassed destination lock")
	}
	if names := rotatedNames(t, parent, rotationID(t, f)); len(names) != 0 {
		t.Fatal("blocked backup created a snapshot", names)
	}
}

func TestRetentionRejectsUnsafeDirectoriesAndMarkerSchemas(t *testing.T) {
	f := newFixture(t)
	parent := privateBackups(t)
	if _, err := ScheduledBackup(t.Context(), f.cfg, f.store, f.objects, parent, -1, io.Discard); err == nil {
		t.Fatal("negative retention accepted")
	}
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := ScheduledBackup(t.Context(), f.cfg, f.store, f.objects, parent, 1, io.Discard); err == nil {
		t.Fatal("public backup destination accepted")
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "backups")
	if err := os.Symlink(parent, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, link + "/", link + "/."} {
		if _, err := ScheduledBackup(t.Context(), f.cfg, f.store, f.objects, path, 1, io.Discard); err == nil {
			t.Fatal("symlink destination accepted", path)
		}
	}
	path := datedBackup(t, f, parent, 2010, false)
	id := rotationID(t, f)
	valid, _ := json.Marshal(retentionMarker{Version: 1, Instance: id})
	for _, bad := range []string{`{"version":2,"instance":"` + id + `"}`, `{"version":1,"instance":"other"}`, `{"version":1,"instance":"` + id + `","extra":true}`, string(valid) + " false", string(valid) + strings.Repeat(" ", 1024)} {
		if err := os.WriteFile(filepath.Join(path, scheduledMarker), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if markedForInstance(path, id) {
			t.Fatalf("invalid marker accepted: %q", bad)
		}
	}
}

func TestScheduledBackupRefusesSymlinkLockWithoutTouchingTarget(t *testing.T) {
	f := newFixture(t)
	parent := privateBackups(t)
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("keep this file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(parent, ".imvault-backup.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := ScheduledBackup(t.Context(), f.cfg, f.store, f.objects, parent, 1, io.Discard); err == nil {
		t.Fatal("symlink backup lock accepted")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "keep this file" {
		t.Fatal("lock target changed", err)
	}
}
