// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"strings"
	"testing"
)

func TestUniqueViolationsAreToldApartFromOtherConstraints(t *testing.T) {
	s, ctx := newTestStore(t)
	mustUser(t, s, ctx, "taken")
	_, err := s.db.ExecContext(ctx, `INSERT INTO users (username, password_hash, created_at) VALUES ('taken', 'x', 0)`)
	if ok, column := isUniqueViolation(err); !ok || !strings.Contains(column, "username") {
		t.Fatalf("a duplicate username was not recognised: %v (%q)", err, column)
	}
	for name, statement := range map[string]string{
		"check":       `UPDATE users SET can_invite = 7`,
		"foreign key": `INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ('t', 999999, 0, 0)`,
		"not null":    `INSERT INTO users (username, password_hash, created_at) VALUES (NULL, 'x', 0)`,
	} {
		_, err := s.db.ExecContext(ctx, statement)
		if err == nil {
			t.Fatalf("%s: the statement succeeded", name)
		}
		if ok, _ := isUniqueViolation(err); ok {
			t.Errorf("a %s failure was read as a uniqueness conflict: %v", name, err)
		}
	}
	if ok, _ := isUniqueViolation(nil); ok {
		t.Error("no error was read as a conflict")
	}
}

func TestTagLookupsUseTheNamespaceIndexes(t *testing.T) {
	s, ctx := newTestStore(t)
	owner := int64(1)
	for _, ownerID := range []*int64{&owner, nil} {
		namespace, args := namespaceClause("t", ownerID)
		rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT id FROM tags t WHERE `+namespace+` AND t.slug = ?`, append(args, "beach")...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail + "\n")
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan.String(), "INDEX idx_tags_") {
			t.Errorf("owner %v: the lookup scans the table:\n%s", ownerID, plan.String())
		}
	}
}
