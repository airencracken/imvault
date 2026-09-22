// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"imvault/internal/config"
)

type S3 struct {
	client         *s3.Client
	bucket, prefix string
}

func New(ctx context.Context, cfg config.Storage) (Backend, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Driver != "s3" {
		return NewDisk(cfg.Directory)
	}
	return NewS3(ctx, cfg)
}

func NewS3(ctx context.Context, cfg config.Storage) (*S3, error) {
	if cfg.Driver != "s3" {
		return nil, errors.New("S3 backend requires s3 storage configuration")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = timeout
	options := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithHTTPClient(&http.Client{Transport: transport, Timeout: timeout}),
		awsconfig.WithRetryMaxAttempts(3),
	}
	if cfg.AccessKey != "" {
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, cfg.SessionToken)))
	}
	ac, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("S3 configuration: %w", err)
	}
	client := s3.NewFromConfig(ac, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.PathStyle
		// Avoid optional AWS checksum extensions on compatible endpoints. We
		// independently hash every object during migration and backup/restore.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	prefix := cfg.Prefix
	if prefix != "" {
		prefix += "/"
	}
	return &S3{client: client, bucket: cfg.Bucket, prefix: prefix}, nil
}

func (s *S3) key(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	return s.prefix + key, nil
}

func s3Error(err error) error {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return ErrNotFound
		}
	}
	return err // In particular, never treat AccessDenied as a missing object.
}

func (s *S3) head(ctx context.Context, key string) (*s3.HeadObjectOutput, error) {
	full, err := s.key(key)
	if err != nil {
		return nil, err
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &full})
	return out, s3Error(err)
}

func (s *S3) Stat(ctx context.Context, key string) (int64, error) {
	out, err := s.head(ctx, key)
	if err != nil {
		return 0, err
	}
	if out.ContentLength == nil || *out.ContentLength < 0 {
		return 0, errors.New("S3 returned no valid object size")
	}
	return aws.ToInt64(out.ContentLength), nil
}

func (s *S3) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	out, err := s.head(ctx, key)
	if err != nil {
		return nil, err
	}
	if out.ContentLength == nil || *out.ContentLength < 0 {
		return nil, errors.New("S3 returned no valid object size")
	}
	if aws.ToString(out.ETag) == "" {
		return nil, errors.New("S3 returned no object ETag")
	}
	return &s3Reader{ctx: ctx, store: s, key: s.prefix + key, size: *out.ContentLength, etag: out.ETag}, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	full, err := s.key(key)
	if err != nil {
		return err
	}
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &full})
	err = s3Error(err)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (s *S3) LocalPath(string) (string, bool) { return "", false }

// Save spools with bounded memory before publishing. This gives the SDK a
// replayable body for signing/retries, and a failed source never replaces an
// existing object. Scratch files are removed on every exit path.
func (s *S3) Save(ctx context.Context, key string, src io.Reader) (int64, error) {
	full, err := s.key(key)
	if err != nil {
		return 0, err
	}
	f, err := os.CreateTemp("", "imvault-s3-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	n, err := io.Copy(f, ContextReader(ctx, src))
	if err != nil {
		return 0, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if n > 64<<20 {
		err = s.multipart(ctx, full, f, n)
	} else {
		_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &full, Body: f, ContentLength: &n})
	}
	if err != nil {
		return 0, fmt.Errorf("S3 save: %w", err)
	}
	return n, nil
}

func (s *S3) multipart(ctx context.Context, key string, f *os.File, size int64) error {
	start, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		if complete {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		s.client.AbortMultipartUpload(cleanup, &s3.AbortMultipartUploadInput{Bucket: &s.bucket, Key: &key, UploadId: start.UploadId})
	}()
	parts, err := s.uploadParts(ctx, key, start.UploadId, f, size)
	if err != nil {
		return err
	}
	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: &s.bucket, Key: &key, UploadId: start.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	complete = err == nil
	return err
}

func (s *S3) uploadParts(ctx context.Context, key string, id *string, f *os.File, size int64) ([]types.CompletedPart, error) {
	// At most 10,000 parts, each at least 16 MiB except the final part.
	partSize := max(int64(16<<20), (size+9999)/10000)
	var parts []types.CompletedPart
	for offset := int64(0); offset < size; offset += partSize {
		n := min(partSize, size-offset)
		number := int32(len(parts) + 1)
		out, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket: &s.bucket, Key: &key, UploadId: id, PartNumber: &number,
			Body: io.NewSectionReader(f, offset, n), ContentLength: &n,
		})
		if err != nil {
			return nil, err
		}
		parts = append(parts, types.CompletedPart{ETag: out.ETag, PartNumber: &number})
	}
	return parts, nil
}
