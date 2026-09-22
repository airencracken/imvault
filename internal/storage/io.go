// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
)

// ValidateKey accepts portable, relative object keys, including legacy layouts.
func ValidateKey(key string) error {
	if key == "" || key == "." || strings.HasPrefix(key, "/") ||
		strings.ContainsAny(key, "\\\x00") || path.Clean(key) != key ||
		key == ".." || strings.HasPrefix(key, "../") {
		return errors.New("storage: invalid object key")
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func ContextReader(ctx context.Context, r io.Reader) io.Reader {
	return contextReader{ctx, r}
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
