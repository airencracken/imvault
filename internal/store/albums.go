// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"strings"

	"imvault/internal/ids"
	"imvault/internal/models"
)

const albumColumns = `a.id, a.user_id, a.title, a.slug, a.description, a.visibility, a.access, a.created_at,
	COALESCE(u.username, ''), (SELECT COUNT(*) FROM album_files WHERE album_id = a.id)`

func scanAlbum(sc rowScanner) (*models.Album, error) {
	var (
		a          models.Album
		visibility string
		access     string
		created    int64
	)
	if err := sc.Scan(&a.ID, &a.UserID, &a.Title, &a.Slug, &a.Description,
		&visibility, &access, &created, &a.Username, &a.FileCount); err != nil {
		return nil, err
	}
	a.Visibility = models.ParseVisibility(visibility)
	a.Access = models.ParseAlbumAccess(access)
	a.CreatedAt = toTime(created)
	return &a, nil
}

// CreateAlbum inserts an album, deriving a unique slug from the title.
func (s *Store) CreateAlbum(ctx context.Context, userID int64, title, description string, visibility models.Visibility, access models.AlbumAccess) (*models.Album, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("album title is empty")
	}
	if len(title) > 120 {
		title = models.Truncate(title, 120)
	}
	if !visibility.Valid() {
		visibility = models.VisibilityPrivate
	}
	if !access.Valid() {
		access = models.AlbumAccessOwner
	}

	base := ids.Slug(title)
	created := nowUnix()

	var album models.Album
	for attempt := 0; attempt < 8; attempt++ {
		slug := base
		if attempt > 0 {
			slug = fmt.Sprintf("%s-%s", base, ids.New(4))
		}

		res, err := s.db.ExecContext(ctx, `
			INSERT INTO albums (user_id, title, slug, description, visibility, access, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			userID, title, slug, description, string(visibility), string(access), created,
		)
		if err != nil {
			if ok, col := isUniqueViolation(err); ok && strings.Contains(col, "slug") {
				continue // slug collision, try a suffixed one
			}
			return nil, fmt.Errorf("insert album: %w", err)
		}

		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("album id: %w", err)
		}
		album = models.Album{
			ID:          id,
			UserID:      userID,
			Title:       title,
			Slug:        slug,
			Description: description,
			Visibility:  visibility,
			Access:      access,
			CreatedAt:   toTime(created),
		}
		return &album, nil
	}

	return nil, fmt.Errorf("could not allocate a unique album slug for %q", title)
}

// AlbumByID loads an album by primary key.
func (s *Store) AlbumByID(ctx context.Context, id int64) (*models.Album, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+albumColumns+` FROM albums a LEFT JOIN users u ON u.id = a.user_id WHERE a.id = ?`, id)
	a, err := scanAlbum(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return a, nil
}

// AlbumBySlug loads an album by its URL slug.
func (s *Store) AlbumBySlug(ctx context.Context, slug string) (*models.Album, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+albumColumns+` FROM albums a LEFT JOIN users u ON u.id = a.user_id WHERE a.slug = ?`, slug)
	a, err := scanAlbum(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return a, nil
}

// AlbumsByUser lists an account's albums, newest first.
func (s *Store) AlbumsByUser(ctx context.Context, userID int64) ([]*models.Album, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+albumColumns+`
		 FROM albums a LEFT JOIN users u ON u.id = a.user_id
		 WHERE a.user_id = ?
		 ORDER BY a.created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list albums: %w", err)
	}
	defer rows.Close()

	var albums []*models.Album
	for rows.Next() {
		a, err := scanAlbum(rows)
		if err != nil {
			return nil, fmt.Errorf("scan album: %w", err)
		}
		albums = append(albums, a)
	}
	return albums, rows.Err()
}

// UpdateAlbum changes an album's mutable fields.
func (s *Store) UpdateAlbum(ctx context.Context, id int64, title, description string, visibility models.Visibility, access models.AlbumAccess) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("album title is empty")
	}
	if len(title) > 120 {
		title = models.Truncate(title, 120)
	}
	if !visibility.Valid() {
		visibility = models.VisibilityPrivate
	}
	if !access.Valid() {
		access = models.AlbumAccessOwner
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE albums SET title = ?, description = ?, visibility = ?, access = ? WHERE id = ?`,
		title, description, string(visibility), string(access), id); err != nil {
		return fmt.Errorf("update album: %w", err)
	}
	return nil
}

// DeleteAlbum removes an album. Files inside it are unaffected.
func (s *Store) DeleteAlbum(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM albums WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete album: %w", err)
	}
	return nil
}

// AddFileToAlbum is idempotent.
func (s *Store) AddFileToAlbum(ctx context.Context, albumID int64, fileID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO album_files (album_id, file_id, added_at) VALUES (?, ?, ?)
		ON CONFLICT (album_id, file_id) DO NOTHING`,
		albumID, fileID, nowUnix())
	if err != nil {
		return fmt.Errorf("add file to album: %w", err)
	}
	return nil
}

// RemoveFileFromAlbum detaches a file from an album.
func (s *Store) RemoveFileFromAlbum(ctx context.Context, albumID int64, fileID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM album_files WHERE album_id = ? AND file_id = ?`, albumID, fileID); err != nil {
		return fmt.Errorf("remove file from album: %w", err)
	}
	return nil
}

// AlbumMembership returns the set of file ids currently in an album.
func (s *Store) AlbumMembership(ctx context.Context, albumID int64) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_id FROM album_files WHERE album_id = ?`, albumID)
	if err != nil {
		return nil, fmt.Errorf("album membership: %w", err)
	}
	defer rows.Close()

	members := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan album member: %w", err)
		}
		members[id] = true
	}
	return members, rows.Err()
}

// AlbumsForFile lists the albums a file belongs to.
func (s *Store) AlbumsForFile(ctx context.Context, fileID string) ([]*models.Album, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+albumColumns+`
		 FROM albums a
		 LEFT JOIN users u ON u.id = a.user_id
		 JOIN album_files af ON af.album_id = a.id
		 WHERE af.file_id = ?
		 ORDER BY a.title COLLATE NOCASE`, fileID)
	if err != nil {
		return nil, fmt.Errorf("albums for file: %w", err)
	}
	defer rows.Close()

	var albums []*models.Album
	for rows.Next() {
		a, err := scanAlbum(rows)
		if err != nil {
			return nil, fmt.Errorf("scan album: %w", err)
		}
		albums = append(albums, a)
	}
	return albums, rows.Err()
}

// AlbumsVisibleTo lists albums the viewer may see but does not own, newest
// first.
//
// This is how a shared album is discovered. Without it the only way to reach
// somebody else's album would be to be handed its link, which is not a
// collection a group can actually use.
func (s *Store) AlbumsVisibleTo(ctx context.Context, viewerID int64) ([]*models.Album, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+albumColumns+`
		 FROM albums a
		 LEFT JOIN users u ON u.id = a.user_id
		 WHERE a.user_id <> ?
		   AND a.visibility IN ('public', 'members')
		 ORDER BY a.created_at DESC, a.id DESC
		 LIMIT 100`, viewerID)
	if err != nil {
		return nil, fmt.Errorf("albums visible to: %w", err)
	}
	defer rows.Close()

	var albums []*models.Album
	for rows.Next() {
		a, err := scanAlbum(rows)
		if err != nil {
			return nil, fmt.Errorf("scan album: %w", err)
		}
		albums = append(albums, a)
	}
	return albums, rows.Err()
}
