// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"imvault/internal/metadata"
	"imvault/internal/models"
	"imvault/internal/storage"
)

func filteredKeyFor(original string, camera bool) string {
	kind := ".location-v1"
	if camera {
		kind = ".camera-v1"
	}
	ext := path.Ext(original)
	return strings.TrimSuffix(original, ext) + kind + ext
}

func (s *Server) filteredObject(ctx context.Context, file *models.File, camera bool) (string, error) {
	unlock, err := s.content.acquire(ctx, file.SHA256)
	if err != nil {
		return "", err
	}
	defer unlock()
	blob, err := s.store.BlobBySHA(ctx, file.SHA256)
	if err != nil {
		return "", err
	}
	key := blob.LocationKey
	if camera {
		key = blob.CameraKey
	}
	if key != "" {
		if _, err := s.objects.Stat(ctx, key); err == nil {
			return key, nil
		} else if !errors.Is(err, storage.ErrNotFound) {
			return "", err
		}
	}
	src, err := s.objects.Open(ctx, blob.ObjectKey)
	if err != nil {
		return "", err
	}
	defer s.closeLogged(src, "original")
	data, err := io.ReadAll(storage.ContextReader(ctx, src))
	if err != nil {
		return "", err
	}
	filtered, ok := metadata.Filter(data, file.Mime, camera, !camera)
	if !ok {
		return "", fmt.Errorf("cannot filter metadata in %s", file.Ext)
	}
	key = filteredKeyFor(blob.ObjectKey, camera)
	if _, err := s.objects.Save(ctx, key, bytes.NewReader(filtered)); err != nil {
		return "", err
	}
	if err := s.store.SetBlobFilteredKey(ctx, blob.SHA256, key, camera); err != nil {
		return "", err
	}
	return key, nil
}
