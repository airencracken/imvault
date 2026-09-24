// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// SaveBrandingAssets replaces or removes custom public identity images in one
// transaction. A nil image with no remove flag leaves that asset unchanged.
func (s *Store) SaveBrandingAssets(ctx context.Context, mascot, favicon []byte, removeMascot, removeFavicon bool) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, asset := range []struct {
			name   string
			data   []byte
			remove bool
		}{{"mascot", mascot, removeMascot}, {"favicon", favicon, removeFavicon}} {
			if asset.remove {
				if _, err := tx.ExecContext(ctx, `DELETE FROM branding_assets WHERE name = ?`, asset.name); err != nil {
					return fmt.Errorf("remove %s image: %w", asset.name, err)
				}
			} else if len(asset.data) > 0 {
				if _, err := tx.ExecContext(ctx, `INSERT INTO branding_assets(name, content) VALUES (?, ?)
					ON CONFLICT(name) DO UPDATE SET content = excluded.content`, asset.name, asset.data); err != nil {
					return fmt.Errorf("save %s image: %w", asset.name, err)
				}
			}
		}
		return nil
	})
}

// BrandingAsset returns a custom PNG image. The web layer normalizes uploads to
// PNG before they reach the store.
func (s *Store) BrandingAsset(ctx context.Context, name string) ([]byte, error) {
	if name != "mascot" && name != "favicon" {
		return nil, ErrNotFound
	}
	var content []byte
	if err := s.db.QueryRowContext(ctx, `SELECT content FROM branding_assets WHERE name = ?`, name).Scan(&content); err != nil {
		return nil, mapErr(err)
	}
	return content, nil
}

// BrandingAssetState reports which optional images have been uploaded.
func (s *Store) BrandingAssetState(ctx context.Context) (mascot, favicon bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM branding_assets WHERE name = 'mascot'),
		EXISTS(SELECT 1 FROM branding_assets WHERE name = 'favicon')`).Scan(&mascot, &favicon)
	return
}
