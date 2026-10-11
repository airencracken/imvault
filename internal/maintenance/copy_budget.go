// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"imvault/internal/storage"
)

func remainingCopyBudget(stage string, limit int64) (*int64, error) {
	if limit == 0 {
		return nil, nil
	}
	for _, name := range []string{"imvault.db", "secret.key"} {
		info, err := os.Stat(filepath.Join(stage, name))
		if err != nil {
			return nil, err
		}
		if info.Size() > limit {
			return nil, errors.New("backup exceeds copy cap")
		}
		limit -= info.Size()
	}
	return &limit, nil
}

type copyBudgetBackend struct {
	storage.Backend
	remaining *int64
}

func (b *copyBudgetBackend) Save(ctx context.Context, key string, r io.Reader) (int64, error) {
	return b.Backend.Save(ctx, key, &copyBudgetReader{r, b.remaining})
}

type copyBudgetReader struct {
	r         io.Reader
	remaining *int64
}

func (r *copyBudgetReader) Read(p []byte) (int, error) {
	if *r.remaining == 0 {
		var probe [1]byte
		n, err := r.r.Read(probe[:])
		if n > 0 {
			return 0, errors.New("backup exceeds copy cap")
		}
		return 0, err
	}
	if int64(len(p)) > *r.remaining {
		p = p[:int(*r.remaining)]
	}
	n, err := r.r.Read(p)
	*r.remaining -= int64(n)
	return n, err
}
