// SPDX-License-Identifier: AGPL-3.0-or-later
package db

import (
	"context"
	"database/sql"

	"imvault/internal/metadata"
)

// Backfill cached dates in bounded batches inside the schema transaction.
// This reads existing metadata only; originals are never opened or changed.
func backfillCaptureDates(ctx context.Context, tx *sql.Tx) error {
	after := ""
	for {
		rows, err := tx.QueryContext(ctx, `SELECT sha256, details_json FROM blobs WHERE sha256 > ? ORDER BY sha256 LIMIT 200`, after)
		if err != nil {
			return err
		}
		type entry struct{ hash, details string }
		var batch []entry
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.hash, &e.details); err != nil {
				_ = rows.Close()
				return err
			}
			batch = append(batch, e)
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		for _, e := range batch {
			if _, err := tx.ExecContext(ctx, `UPDATE blobs SET taken_at = ? WHERE sha256 = ?`, metadata.CaptureDate(e.details), e.hash); err != nil {
				return err
			}
			after = e.hash
		}
		if len(batch) < 200 {
			return nil
		}
	}
}
