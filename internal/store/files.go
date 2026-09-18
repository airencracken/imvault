// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"imvault/internal/models"
)

const fileColumns = `f.id, f.user_id, f.original_name, f.ext, f.mime, f.size,
	f.width, f.height, f.sha256, f.object_key, f.thumb_key, f.preview_key,
	f.is_public, f.kind, f.duration_ms, f.frame_count, f.views, f.created_at, f.expires_at,
	COALESCE(u.username, '')`

const fileFrom = `FROM files f LEFT JOIN users u ON u.id = f.user_id`

func scanFile(sc rowScanner) (*models.File, error) {
	var (
		f        models.File
		userID   sql.NullInt64
		expires  sql.NullInt64
		isPublic int
		kind     string
		created  int64
	)
	if err := sc.Scan(
		&f.ID, &userID, &f.OriginalName, &f.Ext, &f.Mime, &f.Size,
		&f.Width, &f.Height, &f.SHA256, &f.ObjectKey, &f.ThumbKey, &f.PreviewKey,
		&isPublic, &kind, &f.DurationMS, &f.FrameCount, &f.Views, &created, &expires, &f.Username,
	); err != nil {
		return nil, err
	}
	if userID.Valid {
		id := userID.Int64
		f.UserID = &id
	}
	f.IsPublic = isPublic != 0
	f.Kind = models.ParseKind(kind)
	f.CreatedAt = toTime(created)
	f.ExpiresAt = timePtr(expires)
	return &f, nil
}

// CreateFile inserts a file record.
func (s *Store) CreateFile(ctx context.Context, f *models.File) error {
	kind := string(f.Kind)
	if kind == "" {
		kind = string(models.KindImage)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO files (
			id, user_id, original_name, ext, mime, size, width, height, sha256,
			object_key, thumb_key, preview_key, is_public, kind, duration_ms,
			frame_count, views, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, nullableInt64(f.UserID), f.OriginalName, f.Ext, f.Mime, f.Size,
		f.Width, f.Height, f.SHA256, f.ObjectKey, f.ThumbKey, f.PreviewKey,
		boolToInt(f.IsPublic), kind, f.DurationMS, f.FrameCount, f.Views,
		ts(f.CreatedAt), nullableTime(f.ExpiresAt),
	)
	if err != nil {
		if ok, _ := isUniqueViolation(err); ok {
			return fmt.Errorf("%w: file %s", ErrConflict, f.ID)
		}
		return fmt.Errorf("insert file: %w", err)
	}
	return nil
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

// FileByID loads a single file together with its tags.
func (s *Store) FileByID(ctx context.Context, id string) (*models.File, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+fileColumns+` `+fileFrom+` WHERE f.id = ?`, id)
	f, err := scanFile(row)
	if err != nil {
		return nil, mapErr(err)
	}
	tags, err := s.TagsForFile(ctx, id)
	if err != nil {
		return nil, err
	}
	f.Tags = tags
	return f, nil
}

// FileQuery describes a filtered, paginated file listing.
type FileQuery struct {
	// OwnerID restricts results to one account.
	OwnerID *int64
	// AnonymousOnly restricts results to uploads with no owner.
	AnonymousOnly bool
	// PublicOnly restricts results to publicly visible files.
	PublicOnly bool
	// PrivateOnly restricts results to files that are not public.
	PrivateOnly bool
	// Kind restricts results to one media kind.
	Kind *models.Kind
	// VisibleTo restricts results to files that are public or owned by the
	// given account. It composes with the other filters.
	VisibleTo *int64
	// AlbumID restricts results to members of an album.
	AlbumID *int64
	// TagID restricts results to files carrying a tag.
	TagID *int64
	// Search matches against the file id and original filename.
	Search string
	// Limit caps the number of rows; defaults to 60.
	Limit int
	// Offset skips rows for pagination.
	Offset int
}

func (q FileQuery) where() (string, []any) {
	var (
		clauses []string
		args    []any
	)

	if q.AlbumID != nil {
		clauses = append(clauses, `f.id IN (SELECT file_id FROM album_files WHERE album_id = ?)`)
		args = append(args, *q.AlbumID)
	}
	if q.TagID != nil {
		clauses = append(clauses, `f.id IN (SELECT file_id FROM file_tags WHERE tag_id = ?)`)
		args = append(args, *q.TagID)
	}
	if q.OwnerID != nil {
		clauses = append(clauses, `f.user_id = ?`)
		args = append(args, *q.OwnerID)
	}
	if q.AnonymousOnly {
		clauses = append(clauses, `f.user_id IS NULL`)
	}

	// Visibility: an explicit viewer scope wins over the public-only shorthand.
	switch {
	case q.VisibleTo != nil:
		clause, extra := visibilityClause("f", q.VisibleTo)
		clauses = append(clauses, clause)
		args = append(args, extra...)
	case q.PublicOnly:
		clause, _ := visibilityClause("f", nil)
		clauses = append(clauses, clause)
	}
	if q.PrivateOnly {
		clauses = append(clauses, `f.is_public = 0`)
	}

	if q.Kind != nil {
		clauses = append(clauses, `f.kind = ?`)
		args = append(args, string(*q.Kind))
	}
	if term := strings.TrimSpace(q.Search); term != "" {
		pattern := "%" + escapeLike(term) + "%"
		clauses = append(clauses, `(f.original_name LIKE ? ESCAPE '\' OR f.id LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern)
	}

	// Retention is checked last so its argument always lines up with the
	// trailing placeholder.
	clauses = append(clauses, expiryClause("f"))
	args = append(args, nowUnix())

	return " WHERE " + strings.Join(clauses, " AND "), args
}

// ListFiles returns files matching q, ordered newest first.
func (s *Store) ListFiles(ctx context.Context, q FileQuery) ([]*models.File, error) {
	if q.Limit <= 0 {
		q.Limit = 60
	}
	where, args := q.where()
	args = append(args, q.Limit, q.Offset)

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+fileColumns+` `+fileFrom+where+` ORDER BY f.created_at DESC, f.id DESC LIMIT ? OFFSET ?`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	var files []*models.File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, fmt.Errorf("scan file: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate files: %w", err)
	}
	if err := s.attachTags(ctx, files); err != nil {
		return nil, err
	}
	return files, nil
}

// CountFiles returns how many files match q, ignoring pagination.
func (s *Store) CountFiles(ctx context.Context, q FileQuery) (int, error) {
	where, args := q.where()
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM files f`+where, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count files: %w", err)
	}
	return n, nil
}

// attachTags loads tags for a batch of files in one query.
func (s *Store) attachTags(ctx context.Context, files []*models.File) error {
	if len(files) == 0 {
		return nil
	}

	byID := make(map[string]*models.File, len(files))
	args := make([]any, 0, len(files))
	for _, f := range files {
		byID[f.ID] = f
		args = append(args, f.ID)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT ft.file_id, t.id, t.name, t.slug
		FROM file_tags ft
		JOIN tags t ON t.id = ft.tag_id
		WHERE ft.file_id IN (`+placeholders(len(args))+`)
		ORDER BY t.name COLLATE NOCASE`, args...)
	if err != nil {
		return fmt.Errorf("load file tags: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			fileID string
			t      models.Tag
		)
		if err := rows.Scan(&fileID, &t.ID, &t.Name, &t.Slug); err != nil {
			return fmt.Errorf("scan file tag: %w", err)
		}
		if f, ok := byID[fileID]; ok {
			f.Tags = append(f.Tags, t)
		}
	}
	return rows.Err()
}

// DeleteFile removes a file record. Objects must be deleted separately.
func (s *Store) DeleteFile(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

// SetFilePublic toggles a file's public visibility.
func (s *Store) SetFilePublic(ctx context.Context, id string, public bool) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE files SET is_public = ? WHERE id = ?`, boolToInt(public), id); err != nil {
		return fmt.Errorf("set file visibility: %w", err)
	}
	return nil
}

// IncrementViews bumps a file's view counter.
func (s *Store) IncrementViews(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE files SET views = views + 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("increment views: %w", err)
	}
	return nil
}

// ExpiredFiles returns files whose retention deadline has passed.
func (s *Store) ExpiredFiles(ctx context.Context, at time.Time, limit int) ([]*models.File, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+fileColumns+` `+fileFrom+`
		 WHERE f.expires_at IS NOT NULL AND f.expires_at <= ?
		 ORDER BY f.expires_at ASC LIMIT ?`,
		ts(at), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list expired files: %w", err)
	}
	defer rows.Close()

	var files []*models.File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, fmt.Errorf("scan expired file: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired files: %w", err)
	}
	return files, nil
}

// FileKeys identifies the stored objects belonging to one file, so a caller can
// remove the bytes without loading the whole row.
type FileKeys struct {
	ID      string
	Object  string
	Thumb   string
	Preview string
}

// StoredKeysForUser returns the storage keys of every file an account owns.
//
// The admin "delete user" path needs these before the database rows cascade
// away, since the objects live outside the database.
func (s *Store) StoredKeysForUser(ctx context.Context, userID int64) ([]FileKeys, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, object_key, thumb_key, preview_key FROM files WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("list file keys: %w", err)
	}
	defer rows.Close()

	var keys []FileKeys
	for rows.Next() {
		var k FileKeys
		if err := rows.Scan(&k.ID, &k.Object, &k.Thumb, &k.Preview); err != nil {
			return nil, fmt.Errorf("scan file key: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// escapeLike neutralises LIKE wildcards in user input.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// FileBySHA256 returns any file with the given content hash.
//
// Identical bytes produce identical renditions, so a second upload of the same
// image can reuse everything the first one stored rather than decoding,
// resizing, and writing it all again.
func (s *Store) FileBySHA256(ctx context.Context, sha string) (*models.File, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+fileColumns+` `+fileFrom+` WHERE f.sha256 = ? LIMIT 1`, sha)
	f, err := scanFile(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return f, nil
}

// CountFilesUsingKey counts the file rows referencing a stored object.
//
// It answers "would deleting these bytes strand somebody else's file?", which
// is the question content-addressed storage makes necessary.
func (s *Store) CountFilesUsingKey(ctx context.Context, key string) (int, error) {
	if key == "" {
		return 0, nil
	}

	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM files WHERE object_key = ?)
		  + (SELECT COUNT(*) FROM files WHERE thumb_key = ?)
		  + (SELECT COUNT(*) FROM files WHERE preview_key = ?)`,
		key, key, key).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count files using key: %w", err)
	}
	return n, nil
}
