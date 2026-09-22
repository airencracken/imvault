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

// The storage keys live on the blob rather than on the file, so they are read
// through the content hash. COALESCE keeps a file whose blob is somehow missing
// from breaking every listing that touches it.
const fileColumns = `f.id, f.user_id, f.original_name, f.ext, f.mime, f.size,
	f.width, f.height, f.sha256,
	COALESCE(b.object_key, ''), COALESCE(b.thumb_key, ''), COALESCE(b.preview_key, ''),
	COALESCE(b.details_json, ''),
	f.visibility, f.metadata, f.kind, f.duration_ms, f.frame_count, f.views, f.created_at, f.expires_at,
	COALESCE(u.username, '')`

const fileFrom = `FROM files f
	LEFT JOIN users u ON u.id = f.user_id
	LEFT JOIN blobs b ON b.sha256 = f.sha256`

func scanFile(sc rowScanner) (*models.File, error) {
	var (
		f          models.File
		userID     sql.NullInt64
		expires    sql.NullInt64
		visibility string
		metadata   string
		kind       string
		created    int64
	)
	if err := sc.Scan(
		&f.ID, &userID, &f.OriginalName, &f.Ext, &f.Mime, &f.Size,
		&f.Width, &f.Height, &f.SHA256, &f.ObjectKey, &f.ThumbKey, &f.PreviewKey, &f.Details,
		&visibility, &metadata, &kind, &f.DurationMS, &f.FrameCount, &f.Views, &created, &expires, &f.Username,
	); err != nil {
		return nil, err
	}
	if userID.Valid {
		id := userID.Int64
		f.UserID = &id
	}
	f.Visibility = models.ParseVisibility(visibility)
	f.Metadata = models.ParseMetadataPolicy(metadata)
	f.Kind = models.ParseKind(kind)
	f.CreatedAt = toTime(created)
	f.ExpiresAt = timePtr(expires)
	return &f, nil
}

// CreateFile inserts a file record.
func (s *Store) CreateFile(ctx context.Context, f *models.File) error {
	return s.CreateFileWithLimit(ctx, f, 0)
}

// CreateFileWithLimit checks the instance ceiling in the same statement that
// records the file. Account reservations alone cannot enforce it: anonymous
// uploads have no account, and concurrent reservations precede file inserts.
func (s *Store) CreateFileWithLimit(ctx context.Context, f *models.File, totalLimit int64) error {
	kind := string(f.Kind)
	if kind == "" {
		kind = string(models.KindImage)
	}
	// A caller that did not choose a level gets the closed one rather than an
	// empty string that would fail the check constraint, if there were one.
	if !f.Visibility.Valid() {
		f.Visibility = models.VisibilityPrivate
	}
	if !f.Metadata.Valid() {
		f.Metadata = models.MetadataInherit
	}

	// The blob has to exist first: the trigger that counts references fires on
	// this insert and has nothing to update otherwise.
	query := `
		INSERT INTO files (
			id, user_id, original_name, ext, mime, size, width, height, sha256,
			visibility, metadata, kind, duration_ms, frame_count, views, created_at, expires_at
		) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?`
	args := []any{
		f.ID, nullableInt64(f.UserID), f.OriginalName, f.Ext, f.Mime, f.Size,
		f.Width, f.Height, f.SHA256,
		string(f.Visibility), string(f.Metadata), kind, f.DurationMS, f.FrameCount, f.Views,
		ts(f.CreatedAt), nullableTime(f.ExpiresAt),
	}
	if totalLimit > 0 {
		query += ` WHERE (SELECT COALESCE(SUM(size), 0) FROM files) + ? <= ?`
		args = append(args, f.Size, totalLimit)
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		if ok, _ := isUniqueViolation(err); ok {
			return fmt.Errorf("%w: file %s", ErrConflict, f.ID)
		}
		return fmt.Errorf("insert file: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("insert file rows: %w", err)
	}
	if count == 0 {
		return ErrInstanceFull
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
	// PublicOnly restricts results to the public level.
	PublicOnly bool
	// NotPublic restricts results to anything above the public level. It is
	// what the older public=false filter meant, kept so that callers written
	// against two levels keep working.
	NotPublic bool
	// Visibility restricts results to one exact level.
	Visibility *models.Visibility
	// Kind restricts results to one media kind.
	Kind *models.Kind
	// VisibleTo restricts results to files this account may see: everything
	// public, everything shared with members, and its own. It composes with
	// the other filters.
	VisibleTo *int64
	// AlbumID restricts results to members of an album.
	AlbumID *int64
	// TagID restricts results to files carrying a tag.
	TagID *int64
	// FavoritedBy restricts results to an account's private favorites. Combine
	// with the appropriate visibility scope; a favorite never grants access.
	FavoritedBy *int64
	// Search matches the file id, current filename, and attached tag names/slugs.
	Search string
	// Limit caps the number of rows; defaults to 60.
	Limit int
	// Offset skips rows for pagination.
	Offset int
}

func (q FileQuery) relations() ([]string, []any) {
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
	if q.FavoritedBy != nil {
		clauses = append(clauses, `f.id IN (SELECT file_id FROM favorites WHERE user_id = ?)`)
		args = append(args, *q.FavoritedBy)
	}
	return clauses, args
}

func (q FileQuery) where() (string, []any) {
	clauses, args := q.relations()
	if q.OwnerID != nil {
		clauses = append(clauses, `f.user_id = ?`)
		args = append(args, *q.OwnerID)
	}
	if q.AnonymousOnly {
		clauses = append(clauses, `f.user_id IS NULL`)
	}

	// Visibility: an explicit viewer scope wins over the level shorthands.
	switch {
	case q.VisibleTo != nil:
		clause, extra := visibilityClause("f", q.VisibleTo)
		clauses = append(clauses, clause)
		args = append(args, extra...)
	case q.PublicOnly:
		clauses = append(clauses, `f.visibility = 'public'`)
	}
	if q.NotPublic {
		clauses = append(clauses, `f.visibility <> 'public'`)
	}
	if q.Visibility != nil {
		clauses = append(clauses, `f.visibility = ?`)
		args = append(args, string(*q.Visibility))
	}

	if q.Kind != nil {
		clauses = append(clauses, `f.kind = ?`)
		args = append(args, string(*q.Kind))
	}
	if term := strings.TrimSpace(q.Search); term != "" {
		pattern := "%" + escapeLike(term) + "%"
		clauses = append(clauses, `(f.original_name LIKE ? ESCAPE '\' OR f.id LIKE ? ESCAPE '\'
			OR EXISTS (SELECT 1 FROM file_tags ft JOIN tags t ON t.id = ft.tag_id
				WHERE ft.file_id = f.id AND (t.name LIKE ? ESCAPE '\' OR t.slug LIKE ? ESCAPE '\')))`)
		args = append(args, pattern, pattern, pattern, pattern)
	}

	// Retention is checked last so its argument always lines up with the
	// trailing placeholder.
	clauses = append(clauses, expiryClause("f"))
	args = append(args, nowUnix())

	return " WHERE " + strings.Join(clauses, " AND "), args
}

// ListFiles returns files matching q, ordered newest first. Favorites are
// ordered by when they were saved rather than when the file was uploaded.
func (s *Store) ListFiles(ctx context.Context, q FileQuery) ([]*models.File, error) {
	if q.Limit <= 0 {
		q.Limit = 60
	}
	where, args := q.where()
	order := `f.created_at DESC, f.id DESC`
	if q.FavoritedBy != nil {
		order = `(SELECT created_at FROM favorites WHERE user_id = ? AND file_id = f.id) DESC, f.id DESC`
		args = append(args, *q.FavoritedBy)
	}
	args = append(args, q.Limit, q.Offset)

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+fileColumns+` `+fileFrom+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
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

// RenameFile changes the display/download name without moving shared objects.
func (s *Store) RenameFile(ctx context.Context, id, name string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE files SET original_name = ? WHERE id = ?`, name, id); err != nil {
		return fmt.Errorf("rename file: %w", err)
	}
	return nil
}

// SetFileMetadata changes what happens to a file's metadata.
func (s *Store) SetFileMetadata(ctx context.Context, id string, policy models.MetadataPolicy) error {
	if !policy.Valid() {
		return fmt.Errorf("set file metadata: %q is not a setting", policy)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE files SET metadata = ? WHERE id = ?`, string(policy), id); err != nil {
		return fmt.Errorf("set file metadata: %w", err)
	}
	return nil
}

// SetFileVisibility changes which level a file is visible at.
func (s *Store) SetFileVisibility(ctx context.Context, id string, visibility models.Visibility) error {
	if !visibility.Valid() {
		return fmt.Errorf("set file visibility: %q is not a level", visibility)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE files SET visibility = ? WHERE id = ?`, string(visibility), id); err != nil {
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
