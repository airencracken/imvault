// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"imvault/internal/models"
)

func TestTagSearchRespectsScopeAndDoesNotDuplicateFiles(t *testing.T) {
	s, ctx := newTestStore(t)
	alice, bob := mustUser(t, s, ctx, "alice"), mustUser(t, s, ctx, "bob")
	expired := time.Now().Add(-time.Hour)
	public := mustFile(t, s, ctx, "shared", &alice.ID, models.VisibilityPublic, nil)
	private := mustFile(t, s, ctx, "secret", &alice.ID, models.VisibilityPrivate, nil)
	old := mustFile(t, s, ctx, "expired", &alice.ID, models.VisibilityPublic, &expired)
	for _, file := range []*models.File{public, private, old} {
		for _, tag := range []string{"Summer holidays", "summer family"} {
			if _, err := s.AddTag(ctx, file.ID, file.UserID, tag); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.SetFavorite(ctx, bob.ID, public.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		query FileQuery
		want  int
	}{
		{"owner", FileQuery{OwnerID: &alice.ID, Search: "SUMMER"}, 2},
		{"other gallery", FileQuery{OwnerID: &bob.ID, Search: "summer"}, 0},
		{"public", FileQuery{PublicOnly: true, Search: "summer"}, 1},
		{"viewer", FileQuery{VisibleTo: &bob.ID, Search: "summer"}, 1},
		{"favorites", FileQuery{VisibleTo: &bob.ID, FavoritedBy: &bob.ID, Search: "summer"}, 1},
		{"slug", FileQuery{PublicOnly: true, Search: "summer-holidays"}, 1},
		{"literal percent", FileQuery{PublicOnly: true, Search: "%"}, 0},
		{"literal underscore", FileQuery{PublicOnly: true, Search: "_"}, 0},
		{"literal backslash", FileQuery{PublicOnly: true, Search: `\`}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count, err := s.CountFiles(ctx, tc.query)
			if err != nil || count != tc.want {
				t.Fatalf("count = %d, want %d: %v", count, tc.want, err)
			}
			files, err := s.ListFiles(ctx, tc.query)
			if err != nil || len(files) != tc.want {
				t.Fatalf("list count = %d, want %d: %v", len(files), tc.want, err)
			}
		})
	}
	query := FileQuery{OwnerID: &alice.ID, Search: "summer", Limit: 1}
	first, err := s.ListFiles(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	query.Offset = 1
	second, err := s.ListFiles(ctx, query)
	if err != nil || len(first) != 1 || len(second) != 1 || first[0].ID == second[0].ID {
		t.Fatal("matching several tags duplicated a file across pages")
	}
}
