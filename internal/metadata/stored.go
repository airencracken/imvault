// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"errors"
	"fmt"
	"io"
)

var ErrRead = errors.New("could not read original metadata")

// ExtractStored distinguishes storage outages from unsupported or damaged
// metadata, so an outage never permanently marks a cached extraction complete.
func ExtractStored(src io.ReadSeeker) (*Details, error) {
	r := &storedReader{ReadSeeker: src}
	details, err := Extract(r)
	if r.err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRead, r.err)
	}
	return details, err
}

type storedReader struct {
	io.ReadSeeker
	err error
}

func (r *storedReader) Read(p []byte) (int, error) {
	n, err := r.ReadSeeker.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}

func (r *storedReader) Seek(offset int64, whence int) (int64, error) {
	n, err := r.ReadSeeker.Seek(offset, whence)
	if err != nil {
		r.err = err
	}
	return n, err
}
