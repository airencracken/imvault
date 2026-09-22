// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// s3Reader fetches at most 1 MiB per request. Seeks only change the next range;
// HTTP HEAD and SeekEnd never transfer the object. IfMatch prevents mixing two
// revisions if an operator replaces an object behind the application's back.
type s3Reader struct {
	ctx                     context.Context
	store                   *S3
	key                     string
	size, offset, remaining int64
	etag                    *string
	body                    io.ReadCloser
	closed                  bool
}

func (r *s3Reader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, errors.New("read closed S3 object")
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.offset >= r.size {
		return 0, io.EOF
	}
	if r.body == nil {
		if err := r.openRange(); err != nil {
			return 0, err
		}
	}
	n, err := r.body.Read(p[:min(int64(len(p)), r.remaining)])
	r.offset += int64(n)
	r.remaining -= int64(n)
	if err == io.EOF && r.remaining > 0 {
		err = io.ErrUnexpectedEOF
	}
	if r.remaining == 0 {
		r.closeBody()
		if err == io.EOF {
			err = nil
		}
	}
	return n, err
}

func (r *s3Reader) openRange() error {
	end := r.offset + min(int64(1<<20), r.size-r.offset) - 1
	byteRange := fmt.Sprintf("bytes=%d-%d", r.offset, end)
	out, err := r.store.client.GetObject(r.ctx, &s3.GetObjectInput{
		Bucket: &r.store.bucket, Key: &r.key, Range: &byteRange, IfMatch: r.etag,
	})
	if err != nil {
		return s3Error(err)
	}
	wantRange := fmt.Sprintf("bytes %d-%d/%d", r.offset, end, r.size)
	if aws.ToString(out.ContentRange) != wantRange || aws.ToInt64(out.ContentLength) != end-r.offset+1 {
		out.Body.Close()
		return errors.New("S3 returned an invalid byte range")
	}
	r.body = out.Body
	r.remaining = end - r.offset + 1
	return nil
}

func (r *s3Reader) Seek(offset int64, whence int) (int64, error) {
	if r.closed {
		return 0, errors.New("seek closed S3 object")
	}
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = r.offset
	case io.SeekEnd:
		base = r.size
	default:
		return 0, errors.New("invalid seek origin")
	}
	if offset < -base || offset > math.MaxInt64-base {
		return 0, errors.New("invalid seek offset")
	}
	next := base + offset
	if next != r.offset {
		r.closeBody()
		r.offset = next
	}
	return r.offset, nil
}

func (r *s3Reader) closeBody() {
	if r.body != nil {
		r.body.Close()
		r.body = nil
	}
}

func (r *s3Reader) Close() error {
	r.closeBody()
	r.closed = true
	return nil
}
