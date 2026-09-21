// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"strings"

	"imvault/internal/ids"
	"imvault/internal/models"
)

const albumColumns = `a.id, a.user_id, a.title, a.slug, a.description, a.visibility, a.access, a.metadata, a.created_at,
	COALESCE(u.username, ''), (SELECT COUNT(*) FROM album_files WHERE album_id = a.id)`

func scanAlbum(sc rowScanner) (*models.Album, error) {
	var (
		a          models.Album
		visibility string
		access     string
		metadata   string
		created    int64
	)
	if err := sc.Scan(&a.ID, &a.UserID, &a.Title, &a.Slug, &a.Description,
		&visibility, &access, &metadata, &created, &a.Username, &a.FileCount); err != nil {
		return nil, err
	}
	a.Visibility = models.ParseVisibility(visibility)
	a.Access = models.ParseAlbumAccess(access)
	a.Metadata = models.ParseMetadataPolicy(metadata)
	a.CreatedAt = toTime(created)
	return &a, nil
}

// AlbumInput is the mutable shape of an album: what it takes to create one, and
// what changing one changes.
//
// It is a struct rather than a parameter list because several of the fields are
// the same type, and a caller who swapped two of them would compile and be
// wrong.
type AlbumInput struct {
	Title       string
	Description string
	Visibility  models.Visibility
	Access      models.AlbumAccess
	Metadata    models.MetadataPolicy
}

// normalised fills in anything the caller left out with the closed, cautious
// answer rather than the zero value.
func (in AlbumInput) normalised() AlbumInput {
	in.Title = models.Truncate(strings.TrimSpace(in.Title), 120)
	if !in.Visibility.Valid() {
		in.Visibility = models.VisibilityPrivate
	}
	if !in.Access.Valid() {
		in.Access = models.AlbumAccessOwner
	}
	if !in.Metadata.Valid() {
		in.Metadata = models.MetadataInherit
	}
	return in
}

// CreateAlbum inserts an album, deriving a unique slug from the title.
func (s *Store) CreateAlbum(ctx context.Context, userID int64, in AlbumInput) (*models.Album, error) {
	in = in.normalised()
	if in.Title == "" {
		return nil, fmt.Errorf("album title is empty")
	}

	title := in.Title
	description := in.Description
	visibility := in.Visibility
	access := in.Access
	metadata := in.Metadata

	base := ids.Slug(title)
	created := nowUnix()

	var album models.Album
	for attempt := 0; attempt < 8; attempt++ {
		slug := base
		if attempt > 0 {
			slug = fmt.Sprintf("%s-%s", base, ids.New(4))
		}

		res, err := s.db.ExecContext(ctx, `
			INSERT INTO albums (user_id, title, slug, description, visibility, access, metadata, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			userID, title, slug, description, string(visibility), string(access), string(metadata), created,
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
			Metadata:    metadata,
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
func (s *Store) UpdateAlbum(ctx context.Context, id int64, in AlbumInput) error {
	in = in.normalised()
	if in.Title == "" {
		return fmt.Errorf("album title is empty")
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE albums SET title = ?, description = ?, visibility = ?, access = ?, metadata = ?
		WHERE id = ?`,
		in.Title, in.Description, string(in.Visibility), string(in.Access),
		string(in.Metadata), id); err != nil {
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

// EffectiveMetadataPolicy answers what actually happens to a file's metadata.
//
// It is the most restrictive of the file's own setting resolved against its
// visibility, and the explicit opinion of every album the file is in. An
// album set to "inherit" has no opinion and is skipped, because inheriting
// means deferring to the file, and an album is not where that decision lives.
//
// The arithmetic is deliberately one-directional. Combining never yields
// something more open than any input, so belonging to an album can add caution
// to a file but never remove it. That matters because a shared album's owner
// and a file's owner need not be the same person.
func (s *Store) EffectiveMetadataPolicy(ctx context.Context, file *models.File) (models.MetadataPolicy, error) {
	// The file's own answer, with inherit settled by its visibility.
	opinions := []models.MetadataPolicy{file.Metadata.Resolve(file.Visibility)}

	rows, err := s.db.QueryContext(ctx, `
		SELECT a.metadata FROM albums a
		JOIN album_files af ON af.album_id = a.id
		WHERE af.file_id = ?`, file.ID)
	if err != nil {
		return models.MetadataInherit, fmt.Errorf("album metadata policies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return models.MetadataInherit, fmt.Errorf("scan album metadata: %w", err)
		}
		opinions = append(opinions, models.ParseMetadataPolicy(raw))
	}
	if err := rows.Err(); err != nil {
		return models.MetadataInherit, fmt.Errorf("album metadata policies: %w", err)
	}

	return models.Strictest(opinions...), nil
}
