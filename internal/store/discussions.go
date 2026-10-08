// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"github.com/airencracken/comfylib/reference"
)

func (s *Store) LinkAlbumDiscussion(ctx context.Context, owner, album int64, raw string, remove bool) error {
	if _, err := reference.URL(raw); err != nil {
		return err
	}
	statement := `INSERT INTO album_discussions SELECT id,? FROM albums WHERE id=? AND user_id=? ON CONFLICT DO NOTHING`
	args := []any{raw, album, owner}
	if remove {
		statement = `DELETE FROM album_discussions WHERE url=? AND album_id=? AND EXISTS(SELECT 1 FROM albums WHERE id=album_id AND user_id=?)`
	}
	result, err := s.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		a, err := s.AlbumByID(ctx, album)
		if err != nil || a.UserID != owner {
			return ErrNotFound
		}
	}
	return nil
}
func (s *Store) AlbumDiscussions(ctx context.Context, album int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT url FROM album_discussions WHERE album_id=? ORDER BY url", album)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, rows.Err()
}

type PublicAlbumSummary struct {
	Title, Slug string
	ImageCount  int
}

// PublicAlbumPreview reads access, current title and public-image count in one
// SQLite statement, so a privacy edit cannot interleave separate metadata reads.
func (s *Store) PublicAlbumPreview(ctx context.Context, viewer, album int64, admin bool) (PublicAlbumSummary, error) {
	var out PublicAlbumSummary
	where, args := (FileQuery{AlbumID: &album, PublicOnly: true}).where()
	args = append(args, album, viewer, admin)
	err := s.db.QueryRowContext(ctx, `SELECT a.title,a.slug,(SELECT count(*) FROM files f`+where+` AND f.kind IN ('image','animated')) FROM albums a WHERE a.id=? AND a.visibility = 'public' AND (a.user_id=? OR ?=1)`, args...).Scan(&out.Title, &out.Slug, &out.ImageCount)
	if err != nil {
		return PublicAlbumSummary{}, mapErr(err)
	}
	return out, nil
}
