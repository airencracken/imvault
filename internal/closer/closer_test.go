// SPDX-License-Identifier: AGPL-3.0-or-later

package closer

import (
	"errors"
	"testing"
)

type recorder struct {
	closed bool
	err    error
}

func (r *recorder) Close() error {
	r.closed = true
	return r.err
}

func TestDiscardClosesEvenWhenClosingFails(t *testing.T) {
	for _, err := range []error{nil, errors.New("close failed")} {
		r := &recorder{err: err}
		Discard(r)
		if !r.closed {
			t.Fatalf("Discard did not close (error %v)", err)
		}
	}
}
