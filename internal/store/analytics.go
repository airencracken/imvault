// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"github.com/airencracken/comfylib/analytics"
	"time"
)

func (s *Store) AnalyticsDB() *sql.DB { return s.db }
func (s *Store) Analytics(ctx context.Context, now time.Time) (analytics.Summary, error) {
	return analytics.Collect(ctx, s.db, "SELECT id,username FROM users", now)
}
