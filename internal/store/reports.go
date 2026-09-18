// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"

	"imvault/internal/models"
)

const reportColumns = `r.id, r.target_kind, r.target_id, r.reporter_id, r.reporter, r.reason,
	r.note, r.status, r.created_at, r.resolved_by, COALESCE(u.username, ''),
	r.resolved_at, r.resolution`

const reportFrom = `FROM reports r LEFT JOIN users u ON u.id = r.resolved_by`

func scanReport(sc rowScanner) (*models.Report, error) {
	var (
		report     models.Report
		reporterID sql.NullInt64
		resolvedBy sql.NullInt64
		resolvedAt sql.NullInt64
		kind       string
		reason     string
		status     string
		created    int64
	)
	if err := sc.Scan(&report.ID, &kind, &report.TargetID, &reporterID, &report.Reporter,
		&reason, &report.Note, &status, &created, &resolvedBy, &report.Resolver,
		&resolvedAt, &report.Resolution); err != nil {
		return nil, err
	}
	if reporterID.Valid {
		id := reporterID.Int64
		report.ReporterID = &id
	}
	if resolvedBy.Valid {
		id := resolvedBy.Int64
		report.ResolvedBy = &id
	}
	report.TargetKind = models.TargetKind(kind)
	report.Reason = models.ParseReportReason(reason)
	report.Status = models.ReportStatus(status)
	report.CreatedAt = toTime(created)
	report.ResolvedAt = timePtr(resolvedAt)
	return &report, nil
}

// CreateReport records a member's complaint about a file or an album.
//
// It returns ErrConflict when this account already has an open report on the
// same target. That is enforced by a partial unique index rather than by a check
// here, so two clicks racing cannot both land.
func (s *Store) CreateReport(ctx context.Context, targetKind models.TargetKind, targetID string,
	reporterID int64, reporter string, reason models.ReportReason, note string) (*models.Report, error) {

	if !reason.Valid() {
		reason = models.ReportOther
	}
	created := nowUnix()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO reports (target_kind, target_id, reporter_id, reporter, reason, note, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'open', ?)`,
		string(targetKind), targetID, reporterID, reporter, string(reason), note, created)
	if err != nil {
		if ok, _ := isUniqueViolation(err); ok {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("insert report: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("report id: %w", err)
	}

	return &models.Report{
		ID:         id,
		TargetKind: targetKind,
		TargetID:   targetID,
		ReporterID: &reporterID,
		Reporter:   reporter,
		Reason:     reason,
		Note:       note,
		Status:     models.ReportOpen,
		CreatedAt:  toTime(created),
	}, nil
}

// ReportByID loads one report.
func (s *Store) ReportByID(ctx context.Context, id int64) (*models.Report, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+reportColumns+` `+reportFrom+` WHERE r.id = ?`, id)
	report, err := scanReport(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return report, nil
}

// ListReports returns reports with the given status, a status of "" meaning all
// of them, plus the total for that filter.
func (s *Store) ListReports(ctx context.Context, status models.ReportStatus, limit, offset int) ([]*models.Report, int, error) {
	var (
		where string
		args  []any
	)
	if status != "" {
		where = ` WHERE r.status = ?`
		args = append(args, string(status))
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reports r`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count reports: %w", err)
	}

	query := `SELECT ` + reportColumns + ` ` + reportFrom + where
	// A queue is worked from the front, so open reports come oldest first.
	if status == models.ReportOpen {
		query += ` ORDER BY r.created_at, r.id LIMIT ? OFFSET ?`
	} else {
		query += ` ORDER BY r.created_at DESC, r.id DESC LIMIT ? OFFSET ?`
	}
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()

	var reports []*models.Report
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan report: %w", err)
		}
		reports = append(reports, report)
	}
	return reports, total, rows.Err()
}

// CountOpenReports is how many reports are waiting, for the navigation badge.
func (s *Store) CountOpenReports(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reports WHERE status = 'open'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count open reports: %w", err)
	}
	return n, nil
}

// ResolveReport closes a report. The status condition is part of the statement
// so two moderators working the queue cannot both act on the same report, and
// the second learns that rather than overwriting the first.
func (s *Store) ResolveReport(ctx context.Context, id, resolverID int64,
	status models.ReportStatus, resolution string) error {

	if status != models.ReportActioned && status != models.ReportDismissed {
		return fmt.Errorf("resolve report: %q is not a closing status", status)
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE reports
		SET status = ?, resolved_by = ?, resolved_at = ?, resolution = ?
		WHERE id = ? AND status = 'open'`,
		string(status), resolverID, nowUnix(), resolution, id)
	if err != nil {
		return fmt.Errorf("resolve report: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("resolve report: %w", err)
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

// ModerationEntry is one line of the audit trail, as the store deals in it.
type ModerationEntry struct {
	Actor       *models.User
	Action      models.ModerationAction
	TargetKind  models.TargetKind
	TargetID    string
	TargetLabel string
	Reason      string
}

// RecordModeration appends to the audit trail.
//
// It never fails the action it is describing. A removal that happened but was
// not logged is a gap in the record; a removal refused because the log write
// failed would be the wrong way round, so callers log the error and carry on.
func (s *Store) RecordModeration(ctx context.Context, entry ModerationEntry) error {
	var (
		actorID   any
		actorName string
	)
	if entry.Actor != nil {
		actorID = entry.Actor.ID
		actorName = entry.Actor.Username
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO moderation_log
			(actor_id, actor_name, action, target_kind, target_id, target_label, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		actorID, actorName, string(entry.Action), string(entry.TargetKind),
		entry.TargetID, entry.TargetLabel, entry.Reason, nowUnix()); err != nil {
		return fmt.Errorf("record moderation: %w", err)
	}
	return nil
}

// ListModerationLog returns the audit trail, newest first, plus its length.
func (s *Store) ListModerationLog(ctx context.Context, limit, offset int) ([]*models.ModerationEntry, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM moderation_log`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count moderation log: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, actor_id, actor_name, action, target_kind, target_id, target_label, reason, created_at
		FROM moderation_log
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list moderation log: %w", err)
	}
	defer rows.Close()

	var entries []*models.ModerationEntry
	for rows.Next() {
		var (
			entry   models.ModerationEntry
			actorID sql.NullInt64
			action  string
			kind    string
			created int64
		)
		if err := rows.Scan(&entry.ID, &actorID, &entry.ActorName, &action, &kind,
			&entry.TargetID, &entry.TargetLabel, &entry.Reason, &created); err != nil {
			return nil, 0, fmt.Errorf("scan moderation log: %w", err)
		}
		if actorID.Valid {
			id := actorID.Int64
			entry.ActorID = &id
		}
		entry.Action = models.ModerationAction(action)
		entry.TargetKind = models.TargetKind(kind)
		entry.CreatedAt = toTime(created)
		entries = append(entries, &entry)
	}
	return entries, total, rows.Err()
}
