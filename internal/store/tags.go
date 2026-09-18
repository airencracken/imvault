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
	var t models.Tag
	if err := sc.Scan(&t.ID, &t.UserID, &t.Name, &t.Slug, &t.Username); err != nil {
		return nil, err
	}
	return &t, nil
}

// AddTag attaches a tag to a file, creating it in the file owner's namespace if
// needed, and returns the tag.
//
// ownerID must be the file's owner: tags live in the account that owns the
// file, not the account doing the tagging, so an administrator editing
// somebody else's upload does not leave their own labels on it.
func (s *Store) AddTag(ctx context.Context, fileID string, ownerID int64, name string) (*models.Tag, error) {
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

	if owner, err := s.UserByID(ctx, tag.UserID); err == nil {
		tag.Username = owner.Username
	}
	return &tag, nil
}

// lookupTagTx finds a tag by name within one account.
func lookupTagTx(ctx context.Context, tx *sql.Tx, ownerID int64, name string) (*models.Tag, error) {
	var t models.Tag
	err := tx.QueryRowContext(ctx,
		`SELECT id, user_id, name, slug FROM tags
		 WHERE user_id = ? AND name = ? COLLATE NOCASE`, ownerID, name).
		Scan(&t.ID, &t.UserID, &t.Name, &t.Slug)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// insertTagTx creates a tag, deriving a slug that is unique within the account.
func insertTagTx(ctx context.Context, tx *sql.Tx, ownerID int64, name string) (*models.Tag, error) {
	base := ids.Slug(name)

	for attempt := 0; attempt < 8; attempt++ {
		slug := base
		if attempt > 0 {
			slug = fmt.Sprintf("%s-%s", base, ids.New(4))
		}

		res, err := tx.ExecContext(ctx,
			`INSERT INTO tags (user_id, name, slug, created_at) VALUES (?, ?, ?, ?)`,
			ownerID, name, slug, nowUnix())
		if err != nil {
			if ok, col := isUniqueViolation(err); ok {
				// Another row already uses this name or slug.
				if strings.Contains(col, "slug") {
					continue // try a different slug
				}
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

// RemoveTag detaches a tag from a file and prunes it if the owner has no other
// file carrying it.
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

// TagsForFile lists the tags on one file, including their owner.
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

// TagBySlugInUser resolves a tag inside one account's namespace.
func (s *Store) TagBySlugInUser(ctx context.Context, ownerID int64, slug string) (*models.Tag, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+tagColumns+` `+tagFrom+` WHERE t.user_id = ? AND t.slug = ?`, ownerID, slug)
	t, err := scanTag(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return t, nil
}

// TagByRefInUser resolves a tag by numeric id, slug or name within one account.
// The numeric form takes precedence, so a tag literally named "2026" is still
// reachable by name as long as it is not genuinely id 2026.
func (s *Store) TagByRefInUser(ctx context.Context, ownerID int64, ref string) (*models.Tag, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, ErrNotFound
	}

	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return s.userTagBy(ctx, ownerID, `t.id = ?`, id)
	}
	if tag, err := s.userTagBy(ctx, ownerID, `t.slug = ?`, ref); err == nil {
		return tag, nil
	}
	return s.userTagBy(ctx, ownerID, `t.name = ? COLLATE NOCASE`, ref)
}

func (s *Store) userTagBy(ctx context.Context, ownerID int64, where string, arg any) (*models.Tag, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+tagColumns+` `+tagFrom+` WHERE t.user_id = ? AND `+where, ownerID, arg)
	t, err := scanTag(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return t, nil
}

// ListTags returns the tags that label at least one file the viewer can see,
// most used first.
//
// Tags belong to accounts, but the index follows file visibility: you see your
// own tags, plus tags on other people's public uploads. Counts are computed
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
		JOIN users u ON u.id = t.user_id
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
		var t models.Tag
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Slug, &t.Username, &t.Count); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
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
