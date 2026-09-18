// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"imvault/internal/ids"
	"imvault/internal/models"
)

// maxTagNameLen bounds a tag name, in runes.
const maxTagNameLen = 48

const tagColumns = `t.id, t.user_id, t.name, t.slug, COALESCE(u.username, '')`

const tagFrom = `FROM tags t LEFT JOIN users u ON u.id = t.user_id`

func scanTag(sc rowScanner) (*models.Tag, error) {
	var (
		t      models.Tag
		userID sql.NullInt64
	)
	if err := sc.Scan(&t.ID, &userID, &t.Name, &t.Slug, &t.Username); err != nil {
		return nil, err
	}
	if userID.Valid {
		id := userID.Int64
		t.UserID = &id
	}
	return &t, nil
}

// AddTag attaches a tag to a file, creating it in the given namespace if
// needed, and returns the tag.
//
// ownerID is the namespace the tag belongs to: the account that owns the file,
// or nil for an anonymous upload, whose tags live in one shared namespace. An
// administrator tagging somebody else's file adds to *that* file's namespace,
// so their own labels do not end up on it.
func (s *Store) AddTag(ctx context.Context, fileID string, ownerID *int64, name string) (*models.Tag, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("tag name is empty")
	}
	name = models.Truncate(name, maxTagNameLen)

	var tag models.Tag
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		existing, err := lookupTagTx(ctx, tx, ownerID, name)
		switch {
		case err == nil:
			tag = *existing
		case errors.Is(err, sql.ErrNoRows):
			created, err := insertTagTx(ctx, tx, ownerID, name)
			if err != nil {
				return err
			}
			tag = *created
		default:
			return fmt.Errorf("lookup tag: %w", err)
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO file_tags (file_id, tag_id) VALUES (?, ?)
			 ON CONFLICT (file_id, tag_id) DO NOTHING`,
			fileID, tag.ID)
		if err != nil {
			return fmt.Errorf("link tag: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if tag.UserID != nil {
		if owner, err := s.UserByID(ctx, *tag.UserID); err == nil {
			tag.Username = owner.Username
		}
	}
	return &tag, nil
}

// lookupTagTx finds a tag by name within one namespace.
//
// "IS" rather than "=", because the anonymous namespace is spelled NULL and
// `user_id = NULL` is never true.
func lookupTagTx(ctx context.Context, tx *sql.Tx, ownerID *int64, name string) (*models.Tag, error) {
	var (
		t      models.Tag
		userID sql.NullInt64
	)
	err := tx.QueryRowContext(ctx,
		`SELECT id, user_id, name, slug FROM tags
		 WHERE user_id IS ? AND name = ? COLLATE NOCASE`, nullableInt64(ownerID), name).
		Scan(&t.ID, &userID, &t.Name, &t.Slug)
	if err != nil {
		return nil, err
	}
	if userID.Valid {
		id := userID.Int64
		t.UserID = &id
	}
	return &t, nil
}

// insertTagTx creates a tag, deriving a slug that is unique within the
// namespace.
func insertTagTx(ctx context.Context, tx *sql.Tx, ownerID *int64, name string) (*models.Tag, error) {
	base := ids.Slug(name)

	for attempt := 0; attempt < 8; attempt++ {
		slug := base
		if attempt > 0 {
			slug = fmt.Sprintf("%s-%s", base, ids.New(4))
		}

		res, err := tx.ExecContext(ctx,
			`INSERT INTO tags (user_id, name, slug, created_at) VALUES (?, ?, ?, ?)`,
			nullableInt64(ownerID), name, slug, nowUnix())
		if err != nil {
			// Two partial unique indexes guard each namespace, one on the name
			// and one on the slug, so the message has to be read to tell which
			// rule was broken.
			if ok, column := isUniqueViolation(err); ok && mentionsSlug(column, err) {
				continue // try a different slug
			}
			if ok, _ := isUniqueViolation(err); ok {
				return nil, fmt.Errorf("%w: tag name already used", ErrConflict)
			}
			return nil, fmt.Errorf("insert tag: %w", err)
		}

		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("tag id: %w", err)
		}
		return &models.Tag{ID: id, UserID: ownerID, Name: name, Slug: slug}, nil
	}

	return nil, fmt.Errorf("could not allocate a unique tag slug for %q", name)
}

// mentionsSlug reports whether a uniqueness failure was about the slug rather
// than the name, whichever way SQLite phrased it: a column for the composite
// index, an index name for a partial one.
func mentionsSlug(column string, err error) bool {
	if strings.Contains(strings.ToLower(column), "slug") {
		return true
	}
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "slug")
}

// RemoveTag detaches a tag from a file and prunes it if the namespace has no
// other file carrying it.
func (s *Store) RemoveTag(ctx context.Context, fileID string, tagID int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM file_tags WHERE file_id = ? AND tag_id = ?`, fileID, tagID); err != nil {
			return fmt.Errorf("unlink tag: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM tags WHERE id = ? AND NOT EXISTS (SELECT 1 FROM file_tags WHERE tag_id = ?)`,
			tagID, tagID); err != nil {
			return fmt.Errorf("prune tag: %w", err)
		}
		return nil
	})
}

// TagsForFile lists the tags on one file, including their namespace.
func (s *Store) TagsForFile(ctx context.Context, fileID string) ([]models.Tag, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+tagColumns+`
		FROM file_tags ft
		JOIN tags t ON t.id = ft.tag_id
		LEFT JOIN users u ON u.id = t.user_id
		WHERE ft.file_id = ?
		ORDER BY t.name COLLATE NOCASE`, fileID)
	if err != nil {
		return nil, fmt.Errorf("list file tags: %w", err)
	}
	defer rows.Close()

	var tags []models.Tag
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		tags = append(tags, *t)
	}
	return tags, rows.Err()
}

// TagBySlugInNamespace resolves a tag inside one namespace.
func (s *Store) TagBySlugInNamespace(ctx context.Context, ownerID *int64, slug string) (*models.Tag, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+tagColumns+` `+tagFrom+` WHERE t.user_id IS ? AND t.slug = ?`,
		nullableInt64(ownerID), slug)
	t, err := scanTag(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return t, nil
}

// TagByRefInNamespace resolves a tag by numeric id, slug, or name within one
// namespace. The numeric form takes precedence, so a tag literally named "2026"
// is still reachable by name as long as it is not genuinely id 2026.
func (s *Store) TagByRefInNamespace(ctx context.Context, ownerID *int64, ref string) (*models.Tag, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, ErrNotFound
	}

	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return s.tagBy(ctx, ownerID, `t.id = ?`, id)
	}
	if tag, err := s.tagBy(ctx, ownerID, `t.slug = ?`, ref); err == nil {
		return tag, nil
	}
	return s.tagBy(ctx, ownerID, `t.name = ? COLLATE NOCASE`, ref)
}

func (s *Store) tagBy(ctx context.Context, ownerID *int64, where string, arg any) (*models.Tag, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+tagColumns+` `+tagFrom+` WHERE t.user_id IS ? AND `+where,
		nullableInt64(ownerID), arg)
	t, err := scanTag(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return t, nil
}

// ListTags returns the tags that label at least one file the viewer can see,
// most used first.
//
// Tags belong to namespaces, but the index follows file visibility: you see
// your own tags, plus tags on other people's public uploads, which includes the
// shared namespace that anonymous uploads are tagged in. Counts are computed
// over visible files only, so a tag that exists solely on somebody else's
// private upload never appears. A nil viewerID is an anonymous visitor.
func (s *Store) ListTags(ctx context.Context, viewerID *int64, limit int) ([]models.Tag, error) {
	if limit <= 0 {
		limit = 100
	}

	visibility, args := visibilityClause("f", viewerID)
	args = append(args, nowUnix(), limit)

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+tagColumns+`, COUNT(ft.file_id) AS n
		FROM tags t
		LEFT JOIN users u ON u.id = t.user_id
		JOIN file_tags ft ON ft.tag_id = t.id
		JOIN files f ON f.id = ft.file_id
		WHERE `+visibility+` AND `+expiryClause("f")+`
		GROUP BY t.id
		ORDER BY n DESC, t.name COLLATE NOCASE, t.id
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer rows.Close()

	var tags []models.Tag
	for rows.Next() {
		var (
			t      models.Tag
			userID sql.NullInt64
		)
		if err := rows.Scan(&t.ID, &userID, &t.Name, &t.Slug, &t.Username, &t.Count); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		if userID.Valid {
			id := userID.Int64
			t.UserID = &id
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// CountTagVisibleTo counts the files carrying a tag that the viewer can see.
func (s *Store) CountTagVisibleTo(ctx context.Context, tagID int64, viewerID *int64) (int, error) {
	visibility, visibilityArgs := visibilityClause("f", viewerID)

	args := make([]any, 0, len(visibilityArgs)+2)
	args = append(args, tagID)
	args = append(args, visibilityArgs...)
	args = append(args, nowUnix())

	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM file_tags ft
		JOIN files f ON f.id = ft.file_id
		WHERE ft.tag_id = ? AND `+visibility+` AND `+expiryClause("f"), args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count visible files for tag: %w", err)
	}
	return n, nil
}

// PruneUnusedTags deletes tags that no longer label any file.
func (s *Store) PruneUnusedTags(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM tags WHERE NOT EXISTS (SELECT 1 FROM file_tags WHERE tag_id = tags.id)`)
	if err != nil {
		return 0, fmt.Errorf("prune tags: %w", err)
	}
	return res.RowsAffected()
}
