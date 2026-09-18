// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store is the SQLite-backed data access layer.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Sentinel errors returned by store methods.
var (
	// ErrNotFound means no row matched the query.
	ErrNotFound = errors.New("store: not found")
	// ErrConflict means a uniqueness constraint was violated.
	ErrConflict = errors.New("store: conflict")
	// ErrQuotaExceeded means an upload would take an account past its storage cap.
	ErrQuotaExceeded = errors.New("store: storage quota exceeded")
	// ErrLastAdmin means an operation would leave the instance with no
	// administrator, which would lock everybody out of the admin UI.
	ErrLastAdmin = errors.New("store: the last administrator cannot be removed")
)

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// DB exposes the underlying handle for health checks and shutdown.
func (s *Store) DB() *sql.DB { return s.db }

// withTx runs fn inside a transaction. Callers must not issue queries through
// the Store while the transaction is open: the pool holds a single connection.
func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func nowUnix() int64 { return time.Now().UTC().Unix() }

func ts(t time.Time) int64 {
	if t.IsZero() {
		return nowUnix()
	}
	return t.UTC().Unix()
}

func toTime(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}

func timePtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := toTime(v.Int64)
	return &t
}

func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isUniqueViolation reports whether err is a UNIQUE constraint failure, and on
// which column it occurred.
func isUniqueViolation(err error) (bool, string) {
	if err == nil {
		return false, ""
	}
	msg := err.Error()
	if !strings.Contains(msg, "UNIQUE constraint failed") {
		return false, ""
	}
	if i := strings.LastIndex(msg, ":"); i >= 0 {
		return true, strings.TrimSpace(msg[i+1:])
	}
	return true, ""
}

// placeholders renders "?, ?, ?" for n values.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// expiryClause excludes files whose retention window has closed. A lapsed
// upload is treated as gone from the moment it expires, even before the reaper
// has removed the row and the bytes, so listings never show things that the
// detail route would already refuse to serve.
//
// It carries one placeholder argument (the current time).
func expiryClause(alias string) string {
	return fmt.Sprintf("(%s.expires_at IS NULL OR %s.expires_at > ?)", alias, alias)
}

// visibilityClause restricts files to those a viewer may see: anything public,
// plus their own uploads when they are signed in. A nil viewer is an anonymous
// visitor, who sees only public files.
//
// Every place that exposes files or file-derived data must go through this, so
// that listings, counts and the tag index can never disagree with the access
// checks on the routes that serve the bytes.
func visibilityClause(alias string, viewerID *int64) (string, []any) {
	if viewerID == nil {
		return fmt.Sprintf("%s.is_public = 1", alias), nil
	}
	return fmt.Sprintf("(%s.is_public = 1 OR %s.user_id = ?)", alias, alias), []any{*viewerID}
}

func mapErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
