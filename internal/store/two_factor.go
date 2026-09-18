// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	"imvault/internal/models"
)

// RecoveryCodeCount is how many codes are issued at a time.
const RecoveryCodeCount = 10

// BeginTOTP stores a secret without enabling it.
//
// The secret is written as soon as enrolment starts so the setup page survives
// a reload, but totp_enabled stays false: a half-finished enrolment must not be
// mistaken for protection that exists.
func (s *Store) BeginTOTP(ctx context.Context, userID int64, encryptedSecret string) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET totp_secret = ?, totp_enabled = 0, totp_last_step = 0
		WHERE id = ?`, encryptedSecret, userID); err != nil {
		return fmt.Errorf("begin totp: %w", err)
	}
	return nil
}

// EnableTOTP turns on the second factor for an account.
func (s *Store) EnableTOTP(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_enabled = 1, totp_last_step = 0 WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("enable totp: %w", err)
	}
	return nil
}

// DisableTOTP clears the second factor and any recovery codes with it.
//
// Leaving recovery codes behind would be worse than useless: they would still
// be accepted for an account that no longer has a second factor.
func (s *Store) DisableTOTP(ctx context.Context, userID int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE users
			SET totp_secret = '', totp_enabled = 0, totp_last_step = 0
			WHERE id = ?`, userID); err != nil {
			return fmt.Errorf("disable totp: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("clear recovery codes: %w", err)
		}
		return nil
	})
}

// AcceptTOTPStep records the time step of a code that was just accepted.
//
// The update is conditional, so two requests arriving with the same code at the
// same moment cannot both succeed: the second finds the step already recorded
// and is refused.
func (s *Store) AcceptTOTPStep(ctx context.Context, userID int64, step uint64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET totp_last_step = ?
		WHERE id = ? AND totp_last_step < ?`,
		int64(step), userID, int64(step))
	if err != nil {
		return false, fmt.Errorf("accept totp step: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("accept totp step rows: %w", err)
	}
	return affected > 0, nil
}

// ReplaceRecoveryCodes issues a fresh set, invalidating any previous ones.
//
// Only the digests are stored. The plaintext exists once, in the response that
// shows them.
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID int64, hashes []string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("clear recovery codes: %w", err)
		}

		for _, hash := range hashes {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO recovery_codes (user_id, code_hash, created_at) VALUES (?, ?, ?)`,
				userID, hash, nowUnix()); err != nil {
				return fmt.Errorf("insert recovery code: %w", err)
			}
		}
		return nil
	})
}

// ConsumeRecoveryCode spends a recovery code, reporting whether it was valid.
//
// Marking it used and checking it happen in one statement, so the same code
// cannot be spent twice concurrently.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID int64, hash string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE recovery_codes SET used_at = ?
		WHERE user_id = ? AND code_hash = ? AND used_at IS NULL`,
		nowUnix(), userID, hash)
	if err != nil {
		return false, fmt.Errorf("consume recovery code: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("consume recovery code rows: %w", err)
	}
	return affected > 0, nil
}

// RecoveryCodesRemaining counts the unused codes an account has left, which is
// what tells somebody it is time to generate a new set.
func (s *Store) RecoveryCodesRemaining(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL`,
		userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count recovery codes: %w", err)
	}
	return n, nil
}

// HashRecoveryCode returns the digest stored for a recovery code.
//
// A fast hash is right here, unlike for a password: the codes are long and
// random, so there is nothing to guess, and lookup has to stay cheap.
func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(NormaliseRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}

// NormaliseRecoveryCode strips the grouping and case a person may type.
func NormaliseRecoveryCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}

// AccountStatus is the summary the settings pages show.
type AccountStatus struct {
	User *models.User
	// RecoveryLeft counts unused recovery codes, so the page can say when it is
	// time to generate a fresh set.
	RecoveryLeft int
}

// StatusFor gathers what the account settings pages need.
func (s *Store) StatusFor(ctx context.Context, userID int64) (*AccountStatus, error) {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	remaining, err := s.RecoveryCodesRemaining(ctx, userID)
	if err != nil {
		return nil, err
	}

	return &AccountStatus{User: user, RecoveryLeft: remaining}, nil
}
