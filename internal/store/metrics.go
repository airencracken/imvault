// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"github.com/airencracken/comfylib/admin"
	"github.com/airencracken/comfylib/analytics"
	"time"
)

// Metrics returns aggregate instance totals from one bounded read snapshot.
// Authorization is performed by the owner/admin HTTP handler before this call.
func (s *Store) Metrics(ctx context.Context, now time.Time) ([]admin.Metric, error) {
	return admin.Collect(ctx, s.db, []admin.Query{
		{Label: "Members", SQL: "SELECT count(*) FROM users WHERE disabled=0", Detail: "", Bytes: false},
		{Label: "Live files", SQL: "SELECT count(*) FROM files WHERE trashed_at IS NULL", Detail: "", Bytes: false},
		{Label: "Trash files", SQL: "SELECT count(*) FROM files WHERE trashed_at IS NOT NULL", Detail: "", Bytes: false},
		{Label: "Unique originals", SQL: "SELECT count(*) FROM blobs WHERE refcount>0", Detail: "", Bytes: false},
		{Label: "Original storage", SQL: "SELECT coalesce(sum(size),0) FROM blobs WHERE refcount>0", Detail: "Deduplicated originals; previews and backups excluded.", Bytes: true},
		{Label: "New uploads", SQL: "SELECT count(*) FROM files WHERE created_at>=? AND created_at<=?", Detail: "Past 7 UTC days, including files retained in Trash.", Bytes: false, Args: []any{analytics.WindowStart(now).Unix(), now.Unix()}},
		{Label: "Database size", SQL: "SELECT (SELECT page_count FROM pragma_page_count)*(SELECT page_size FROM pragma_page_size)", Bytes: true, Detail: "Main database allocation; temporary logs, cached media and backups are separate."},
	})
}
