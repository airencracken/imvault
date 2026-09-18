// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"strings"

	"imvault/internal/models"
)

// CountUsers returns the total number of registered accounts.
//
// The registration flow uses it to decide whether an account is the first, and
// therefore becomes the administrator.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// NewUser is the set of fields needed to create an account.
type NewUser struct {
	Username     string
	Email        string
	PasswordHash string

	// IsAdmin grants access to the admin UI.
	IsAdmin bool
	// QuotaBytes caps stored original bytes; zero means unlimited.
	QuotaBytes int64
}

// CreateUser inserts a new account. It returns ErrConflict when the username
// or email is already taken.
func (s *Store) CreateUser(ctx context.Context, in NewUser) (*models.User, error) {
	if in.QuotaBytes < 0 {
		in.QuotaBytes = 0
	}
	created := nowUnix()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, email, password_hash, is_admin, quota_bytes, storage_used, created_at)
		VALUES (?, ?, ?, ?, ?, 0, ?)`,
		in.Username, in.Email, in.PasswordHash, boolToInt(in.IsAdmin), in.QuotaBytes, created,
	)
	if err != nil {
		if ok, col := isUniqueViolation(err); ok {
			return nil, fmt.Errorf("%w: %s already taken", ErrConflict, col)
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("user id: %w", err)
	}

	return &models.User{
		ID:           id,
		Username:     in.Username,
		Email:        in.Email,
		PasswordHash: in.PasswordHash,
		IsAdmin:      in.IsAdmin,
		QuotaBytes:   in.QuotaBytes,
		CreatedAt:    toTime(created),
	}, nil
}

const userColumns = `id, username, email, password_hash, is_admin, disabled,
	email_verified, quota_bytes, storage_used, max_file_bytes, created_at,
	totp_secret, totp_enabled, totp_last_step`

func scanUser(sc rowScanner) (*models.User, error) {
	var (
		u             models.User
		isAdmin       int
		disabled      int
		emailVerified int
		totpEnabled   int
		totpLastStep  int64
		created       int64
	)
	if err := sc.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &isAdmin,
		&disabled, &emailVerified, &u.QuotaBytes, &u.StorageUsed, &u.MaxFileBytes,
		&created, &u.TOTPSecret, &totpEnabled, &totpLastStep); err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	u.Disabled = disabled != 0
	u.EmailVerified = emailVerified != 0
	u.TOTPEnabled = totpEnabled != 0
	u.TOTPLastStep = uint64(totpLastStep)
	u.CreatedAt = toTime(created)
	return &u, nil
}

// UserByUsername looks up an account by its (case-insensitive) username.
func (s *Store) UserByUsername(ctx context.Context, username string) (*models.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = ? COLLATE NOCASE`, username)
	u, err := scanUser(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return u, nil
}

// UserByEmail looks up an account by its (case-insensitive) email address.
func (s *Store) UserByEmail(ctx context.Context, email string) (*models.User, error) {
	if strings.TrimSpace(email) == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = ? COLLATE NOCASE`, email)
	u, err := scanUser(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return u, nil
}

// UserByID looks up an account by primary key.
func (s *Store) UserByID(ctx context.Context, id int64) (*models.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return u, nil
}

// ListUsers returns accounts ordered by usage, heaviest first, along with the
// total count for pagination.
func (s *Store) ListUsers(ctx context.Context, limit, offset int) ([]*models.User, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users
		 ORDER BY storage_used DESC, username COLLATE NOCASE
		 LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []*models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

// SetUserQuota changes an account's storage cap. Zero means unlimited.
func (s *Store) SetUserQuota(ctx context.Context, userID, quotaBytes int64) error {
	if quotaBytes < 0 {
		quotaBytes = 0
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET quota_bytes = ? WHERE id = ?`, quotaBytes, userID); err != nil {
		return fmt.Errorf("set quota: %w", err)
	}
	return nil
}

// SetUserFileLimit changes the largest single file an account may upload. Zero
// clears the override, leaving the instance defaults in force.
func (s *Store) SetUserFileLimit(ctx context.Context, userID, maxBytes int64) error {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET max_file_bytes = ? WHERE id = ?`, maxBytes, userID); err != nil {
		return fmt.Errorf("set file limit: %w", err)
	}
	return nil
}

// SetUserDisabled enables or disables an account. A disabled account cannot
// sign in and its API keys stop working.
func (s *Store) SetUserDisabled(ctx context.Context, userID int64, disabled bool) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET disabled = ? WHERE id = ?`, boolToInt(disabled), userID); err != nil {
		return fmt.Errorf("set disabled: %w", err)
	}
	return nil
}

// SetUserAdmin grants or revokes administrator rights.
//
// Revoking the last administrator is refused: it would leave the instance with
// nobody able to reach the admin UI.
func (s *Store) SetUserAdmin(ctx context.Context, userID int64, isAdmin bool) error {
	if !isAdmin {
		admins, err := s.CountAdmins(ctx)
		if err != nil {
			return err
		}
		current, err := s.UserByID(ctx, userID)
		if err != nil {
			return err
		}
		if current.IsAdmin && admins <= 1 {
			return ErrLastAdmin
		}
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET is_admin = ? WHERE id = ?`, boolToInt(isAdmin), userID); err != nil {
		return fmt.Errorf("set admin: %w", err)
	}
	return nil
}

// CountAdmins returns how many enabled administrators exist.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return n, nil
}

// DeleteUser removes an account. The caller is responsible for deleting the
// account's stored objects first; the database rows cascade.
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	admins, err := s.CountAdmins(ctx)
	if err != nil {
		return err
	}
	target, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if target.IsAdmin && admins <= 1 {
		return ErrLastAdmin
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// ReserveStorage atomically claims size bytes for an account, refusing when the
// claim would exceed its quota.
//
// Doing this in a single UPDATE is what makes the check safe under concurrent
// uploads: two requests cannot both observe room that only exists once.
func (s *Store) ReserveStorage(ctx context.Context, userID, size int64) error {
	if size <= 0 {
		return nil
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET storage_used = storage_used + ?
		WHERE id = ? AND (quota_bytes = 0 OR storage_used + ? <= quota_bytes)`,
		size, userID, size)
	if err != nil {
		return fmt.Errorf("reserve storage: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reserve storage rows: %w", err)
	}
	if affected == 0 {
		return ErrQuotaExceeded
	}
	return nil
}

// ReleaseStorage gives size bytes back to an account.
//
// The total is clamped at zero so that a double release, or a delete whose
// reserve never happened, cannot drive usage negative.
func (s *Store) ReleaseStorage(ctx context.Context, userID, size int64) error {
	if size <= 0 {
		return nil
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET storage_used = MAX(0, storage_used - ?) WHERE id = ?`,
		size, userID)
	if err != nil {
		return fmt.Errorf("release storage: %w", err)
	}
	return nil
}

// CheckQuota reports whether an account could store size more bytes, without
// claiming anything. It is a cheap pre-flight so an oversized upload is
// rejected before the work of decoding and writing it.
func (s *Store) CheckQuota(ctx context.Context, userID, size int64) error {
	if size <= 0 {
		return nil
	}

	var (
		quota  int64
		used   int64
		exists bool
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT quota_bytes, storage_used, 1 FROM users WHERE id = ?`, userID).
		Scan(&quota, &used, &exists)
	if err != nil {
		return mapErr(err)
	}
	if quota > 0 && used+size > quota {
		return ErrQuotaExceeded
	}
	return nil
}

// RecomputeStorageUsage recalculates every account's running total from the
// files table and reports how many rows it corrected. It exists so an operator
// can repair drift caused by a crash mid-upload.
func (s *Store) RecomputeStorageUsage(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET storage_used = (
			SELECT COALESCE(SUM(f.size), 0) FROM files f WHERE f.user_id = users.id
		)
		WHERE storage_used <> (
			SELECT COALESCE(SUM(f.size), 0) FROM files f WHERE f.user_id = users.id
		)`)
	if err != nil {
		return 0, fmt.Errorf("recompute storage usage: %w", err)
	}
	return res.RowsAffected()
}

// InstanceStats summarises the whole instance for the admin dashboard.
type InstanceStats struct {
	Users       int
	Admins      int
	Disabled    int
	Files       int
	PublicFiles int
	TotalBytes  int64
	ActiveKeys  int
}

// Stats gathers the instance-wide counts shown on the admin dashboard.
func (s *Store) Stats(ctx context.Context) (InstanceStats, error) {
	var stats InstanceStats

	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM users),
			(SELECT COUNT(*) FROM users WHERE is_admin = 1),
			(SELECT COUNT(*) FROM users WHERE disabled = 1),
			(SELECT COUNT(*) FROM files),
			(SELECT COUNT(*) FROM files WHERE visibility = 'public'),
			(SELECT COALESCE(SUM(size), 0) FROM files),
			(SELECT COUNT(*) FROM api_keys WHERE expires_at IS NULL OR expires_at > ?)`,
		nowUnix(),
	).Scan(&stats.Users, &stats.Admins, &stats.Disabled, &stats.Files,
		&stats.PublicFiles, &stats.TotalBytes, &stats.ActiveKeys)
	if err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: %w", err)
	}
	return stats, nil
}
