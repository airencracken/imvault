// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import "context"

func (s *Store) CaptureYears(ctx context.Context, owner int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT substr(b.taken_at,1,4) AS year FROM files f JOIN blobs b ON b.sha256=f.sha256 WHERE f.user_id=? AND `+expiryClause+` AND b.taken_at <> '' ORDER BY year DESC`, owner, nowUnix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var years []string
	for rows.Next() {
		var year string
		if err := rows.Scan(&year); err != nil {
			return nil, err
		}
		years = append(years, year)
	}
	return years, rows.Err()
}
