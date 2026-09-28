// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxBulkTagFiles = 500
const MaxBulkTags = 50

var ErrInvalidTags = errors.New("invalid bulk tags")

// AddTagsToFiles adds labels without replacing existing ones. Every file must
// belong to the caller, including for administrators. Authorization and writes
// share one transaction: a missing file or failed write changes nothing.
func (s *Store) AddTagsToFiles(ctx context.Context, ownerID int64, fileIDs, names []string) (int, error) {
	files, err := bulkTagValues(fileIDs, MaxBulkTagFiles, 128, "files")
	if err != nil {
		return 0, err
	}
	tags, err := bulkTagValues(names, MaxBulkTags, maxTagNameLen, "tags")
	if err != nil {
		return 0, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		for _, id := range files {
			var found int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM files f WHERE f.id = ? AND f.user_id = ? AND `+expiryClause("f"), id, ownerID, nowUnix()).Scan(&found)
			if err != nil {
				return mapErr(err)
			}
		}
		for _, name := range tags {
			tag, err := lookupTagTx(ctx, tx, &ownerID, name)
			if errors.Is(err, sql.ErrNoRows) {
				tag, err = insertTagTx(ctx, tx, &ownerID, name)
			}
			if err != nil {
				return err
			}
			for _, id := range files {
				if _, err := tx.ExecContext(ctx, `INSERT INTO file_tags (file_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, tag.ID); err != nil {
					return fmt.Errorf("link bulk tag: %w", err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(files), nil
}

func bulkTagValues(values []string, maxCount, maxLength int, field string) ([]string, error) {
	if len(values) == 0 || len(values) > maxCount {
		return nil, fmt.Errorf("%w: choose between 1 and %d %s", ErrInvalidTags, maxCount, field)
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxLength || strings.ContainsFunc(value, unicode.IsControl) {
			return nil, fmt.Errorf("%w: %s must contain 1–%d characters without control characters", ErrInvalidTags, field, maxLength)
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out, nil
}
