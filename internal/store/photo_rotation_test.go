// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"errors"
	"imvault/internal/models"
	"sync"
	"testing"
)

func TestPhotoRotationIsPerFileAtomicAndConcurrent(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := mustUser(t, s, ctx, "alex")
	original := mustFile(t, s, ctx, "first", &owner.ID, models.VisibilityPrivate, nil)
	second := *original
	second.ID = "second"
	if err := s.CreateFile(ctx, &second); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"right", "right", "left"} {
		if err := s.RotateFile(ctx, original.ID, direction); err != nil {
			t.Fatal(err)
		}
	}
	rotated, err := s.FileByID(ctx, original.ID)
	if err != nil || rotated.Rotation != 90 {
		t.Fatal("relative rotation", rotated, err)
	}
	unchanged, err := s.FileByID(ctx, second.ID)
	if err != nil || unchanged.Rotation != 0 || unchanged.ObjectKey != original.ObjectKey {
		t.Fatal("rotation changed shared upload", err)
	}
	if rotated.SHA256 != original.SHA256 || rotated.Width != original.Width || rotated.Size != original.Size {
		t.Fatal("rotation changed original properties")
	}
	if err := s.RotateFile(ctx, original.ID, "reset"); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 19; i++ {
		group.Add(1)
		go func() { defer group.Done(); failures <- s.RotateFile(ctx, original.ID, "right") }()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	rotated, err = s.FileByID(ctx, original.ID)
	if err != nil || rotated.Rotation != 270 {
		t.Fatal("concurrent turns lost", rotated, err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_rotation BEFORE UPDATE OF rotation ON files BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RotateFile(ctx, original.ID, "left"); err == nil {
		t.Fatal("ignored failed write")
	}
	rotated, _ = s.FileByID(ctx, original.ID)
	if rotated.Rotation != 270 {
		t.Fatal("failed rotation changed data")
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_rotation`); err != nil {
		t.Fatal(err)
	}
	if err := s.RotateFile(ctx, original.ID, "../right"); err == nil {
		t.Fatal("invalid direction accepted")
	}
	if err := s.RotateFile(ctx, "absent", "right"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing file", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE files SET rotation=0,kind='animated' WHERE id=?`, original.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RotateFile(ctx, original.ID, "right"); !errors.Is(err, ErrNotFound) {
		t.Fatal("rotated animation", err)
	}
	for _, angle := range []any{nil, -90, 1, 360, 90} {
		if _, err := s.db.ExecContext(ctx, `UPDATE files SET rotation=? WHERE id=?`, angle, original.ID); err == nil {
			t.Fatal("schema accepted invalid animation rotation", angle)
		}
	}
}
