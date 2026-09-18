// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"

	"imvault/internal/models"
)

func TestOneOpenReportPerAccountPerTarget(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")

	report, err := s.CreateReport(ctx, models.TargetFile, "abc123", alice.ID, "alice", models.ReportAbuse, "look")
	if err != nil {
		t.Fatalf("create report: %v", err)
	}

	// The same person reporting the same thing again is not more persuasive,
	// and the unique index is what enforces it rather than a check that two
	// clicks could race past.
	if _, err := s.CreateReport(ctx, models.TargetFile, "abc123", alice.ID, "alice", models.ReportSpam, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate report = %v, want ErrConflict", err)
	}

	// A different person's complaint is a different report.
	if _, err := s.CreateReport(ctx, models.TargetFile, "abc123", bob.ID, "bob", models.ReportSpam, ""); err != nil {
		t.Errorf("a second account could not report the same file: %v", err)
	}

	// A different target is a different report.
	if _, err := s.CreateReport(ctx, models.TargetAlbum, "abc123", alice.ID, "alice", models.ReportSpam, ""); err != nil {
		t.Errorf("the same account could not report an album: %v", err)
	}

	// Closing it lifts the restriction, so somebody may complain again after a
	// dismissal.
	if err := s.ResolveReport(ctx, report.ID, bob.ID, models.ReportDismissed, "no"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := s.CreateReport(ctx, models.TargetFile, "abc123", alice.ID, "alice", models.ReportSpam, ""); err != nil {
		t.Errorf("could not report again after a dismissal: %v", err)
	}
}

func TestResolvingAReportHappensOnce(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")
	bob := mustUser(t, s, ctx, "bob")

	report, err := s.CreateReport(ctx, models.TargetFile, "abc", alice.ID, "alice", models.ReportSpam, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ResolveReport(ctx, report.ID, bob.ID, models.ReportActioned, "removed"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Two moderators working the same queue must not both act on one report.
	// The status condition is part of the statement, so the second learns that
	// somebody got there first rather than overwriting the decision.
	if err := s.ResolveReport(ctx, report.ID, bob.ID, models.ReportDismissed, "changed my mind"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second resolve = %v, want ErrNotFound", err)
	}

	after, err := s.ReportByID(ctx, report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != models.ReportActioned || after.Resolution != "removed" {
		t.Errorf("the report was overwritten: %+v", after)
	}

	// A status that does not close anything is refused rather than quietly
	// reopening the report.
	if err := s.ResolveReport(ctx, report.ID, bob.ID, models.ReportOpen, ""); err == nil {
		t.Error("a report was reopened through the resolve path")
	}
}

func TestTheQueueIsOldestFirstAndTheLogIsNewestFirst(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	for _, id := range []string{"first", "second", "third"} {
		if _, err := s.CreateReport(ctx, models.TargetFile, id, alice.ID, "alice", models.ReportSpam, ""); err != nil {
			t.Fatal(err)
		}
	}

	reports, total, err := s.ListReports(ctx, models.ReportOpen, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(reports) != 3 {
		t.Fatalf("%d reports, want 3", len(reports))
	}
	// A queue is worked from the front, so the oldest complaint comes first.
	if reports[0].TargetID != "first" || reports[2].TargetID != "third" {
		t.Errorf("queue order = %s, %s, %s", reports[0].TargetID, reports[1].TargetID, reports[2].TargetID)
	}

	for _, id := range []string{"a", "b", "c"} {
		if err := s.RecordModeration(ctx, ModerationEntry{
			Actor: alice, Action: models.ActionRemoveFile, TargetKind: models.TargetFile, TargetID: id,
		}); err != nil {
			t.Fatal(err)
		}
	}

	entries, logTotal, err := s.ListModerationLog(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if logTotal != 3 {
		t.Errorf("log total = %d, want 3", logTotal)
	}
	// A log reads backwards: the most recent thing first.
	if entries[0].TargetID != "c" || entries[2].TargetID != "a" {
		t.Errorf("log order = %s, %s, %s", entries[0].TargetID, entries[1].TargetID, entries[2].TargetID)
	}
}

func TestTheAuditTrailKeepsItsAttribution(t *testing.T) {
	s, ctx := newTestStore(t)
	alice := mustUser(t, s, ctx, "alice")

	if err := s.RecordModeration(ctx, ModerationEntry{
		Actor:       alice,
		Action:      models.ActionRemoveAlbum,
		TargetKind:  models.TargetAlbum,
		TargetID:    "holiday",
		TargetLabel: "Holiday 2026",
		Reason:      "reported: Copyright",
	}); err != nil {
		t.Fatal(err)
	}

	// Delete the account. The log has to keep saying who did it, or it stops
	// being an audit trail the moment somebody leaves.
	if err := s.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}

	entries, _, err := s.ListModerationLog(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d entries, want 1", len(entries))
	}
	entry := entries[0]
	if entry.ActorName != "alice" {
		t.Errorf("actor name = %q after the account was deleted", entry.ActorName)
	}
	if entry.ActorID != nil {
		t.Error("the actor id outlived the account it pointed at")
	}
	if entry.TargetLabel != "Holiday 2026" {
		t.Errorf("target label = %q", entry.TargetLabel)
	}
	if entry.Reason != "reported: Copyright" {
		t.Errorf("reason = %q", entry.Reason)
	}
}
