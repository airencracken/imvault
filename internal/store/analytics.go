// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import "database/sql"

// AnalyticsDB is the database the shared analytics tables live in. Imvault
// records nothing identifying in them; it only keeps them pruned.
func (s *Store) AnalyticsDB() *sql.DB { return s.db }
