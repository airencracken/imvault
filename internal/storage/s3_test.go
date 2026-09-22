// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"imvault/internal/config"
)

type s3Fixture struct {
	mu                        sync.Mutex
	data                      map[string][]byte
	parts                     map[int][]byte
	gets, heads, aborts, puts int
	bytesRead                 int
	badRange, deny, failPart  bool
}

func testS3(t *testing.T) (*S3, *s3Fixture) {
	t.Helper()
	f := &s3Fixture{data: map[string][]byte{}, parts: map[int][]byte{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("unsigned S3 request")
		}
		if r.Header.Get("X-Amz-Acl") != "" {
			t.Error("backend should not set object ACLs")
		}
		if !strings.HasPrefix(r.URL.Path, "/bucket/library/") {
			t.Errorf("wrong bucket/prefix: %s", r.URL.Path)
		}
		if f.deny {
			w.WriteHeader(403)
			io.WriteString(w, "<Error><Code>AccessDenied</Code></Error>")
			return
		}
		if r.URL.Query().Has("uploads") || r.URL.Query().Has("uploadId") {
			f.multipart(w, r)
			return
		}
		f.object(w, r)
	}))
	t.Cleanup(server.Close)
	s, err := NewS3(t.Context(), config.Storage{Driver: "s3", Endpoint: server.URL, Region: "auto", Bucket: "bucket", Prefix: "library", AccessKey: "test-key", SecretKey: "test-secret", PathStyle: true, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func (f *s3Fixture) object(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Path
	switch r.Method {
	case "PUT":
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read", 400)
			return
		}
		f.data[key] = data
		f.puts++
		w.Header().Set("ETag", `"stored"`)
	case "DELETE":
		delete(f.data, key)
		w.WriteHeader(204)
	case "GET", "HEAD":
		data, ok := f.data[key]
		if !ok {
			w.WriteHeader(404)
			io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
			return
		}
		sum := sha256.Sum256(data)
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		w.Header().Set("ETag", etag)
		if r.Method == "HEAD" {
			f.heads++
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			return
		}
		if r.Header.Get("If-Match") != etag {
			w.WriteHeader(412)
			return
		}
		f.gets++
		var start, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || start < 0 || end >= len(data) {
			http.Error(w, "bad range", 416)
			return
		}
		if !f.badRange {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		}
		w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
		w.WriteHeader(206)
		f.bytesRead += end - start + 1
		w.Write(data[start : end+1])
	default:
		http.Error(w, "unexpected method", 400)
	}
}

func (f *s3Fixture) multipart(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/xml")
	if q.Has("uploads") {
		io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>test-upload</UploadId></InitiateMultipartUploadResult>`)
		return
	}
	switch r.Method {
	case "PUT":
		if f.failPart {
			http.Error(w, "invalid part", 400)
			return
		}
		number, _ := strconv.Atoi(q.Get("partNumber"))
		f.parts[number], _ = io.ReadAll(r.Body)
		w.Header().Set("ETag", fmt.Sprintf(`"part-%d"`, number))
	case "POST":
		var request struct {
			Parts []struct {
				Number int `xml:"PartNumber"`
			} `xml:"Part"`
		}
		if err := xml.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "XML", 400)
			return
		}
		var result []byte
		for _, part := range request.Parts {
			result = append(result, f.parts[part.Number]...)
		}
		f.data[r.URL.Path] = result
		io.WriteString(w, `<CompleteMultipartUploadResult><ETag>"complete"</ETag></CompleteMultipartUploadResult>`)
	case "DELETE":
		f.aborts++
		f.parts = map[int][]byte{}
		w.WriteHeader(204)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("source failed") }

func TestStorageContract(t *testing.T) {
	for _, driver := range []string{"disk", "s3"} {
		t.Run(driver, func(t *testing.T) {
			var backend Backend
			if driver == "s3" {
				backend, _ = testS3(t)
			} else {
				var err error
				backend, err = NewDisk(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx := t.Context()
			if _, err := backend.Open(ctx, "missing"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing: %v", err)
			}
			if _, err := backend.Save(ctx, "folder/photo", strings.NewReader("abcdef")); err != nil {
				t.Fatal(err)
			}
			if _, err := backend.Save(ctx, "folder/photo", io.MultiReader(strings.NewReader("bad"), brokenReader{})); err == nil {
				t.Fatal("failed source was accepted")
			}
			reader, err := backend.Open(ctx, "folder/photo")
			if err != nil {
				t.Fatal(err)
			}
			if n, err := reader.Seek(-3, io.SeekEnd); n != 3 || err != nil {
				t.Fatal(n, err)
			}
			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil || string(data) != "def" {
				t.Fatalf("seek or atomic save: %q %v", data, err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := backend.Save(cancelled, "folder/photo", strings.NewReader("bad")); err == nil {
				t.Fatal("cancelled save succeeded")
			}
			for _, key := range []string{"", "../escape", "/absolute", "a/../b", "a\\b", "a\x00b"} {
				if _, err := backend.Save(ctx, key, strings.NewReader("bad")); err == nil {
					t.Errorf("unsafe key %q accepted", key)
				}
			}
			if err := backend.Delete(ctx, "folder/photo"); err != nil {
				t.Fatal(err)
			}
			if err := backend.Delete(ctx, "folder/photo"); err != nil {
				t.Fatal("delete not idempotent", err)
			}
		})
	}
}

func TestS3ReadsBoundedRangesAndHandlesRemoteFailures(t *testing.T) {
	s, f := testS3(t)
	data := bytes.Repeat([]byte("01234567"), 1<<20)
	if _, err := s.Save(t.Context(), "clip", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	r, err := s.Open(t.Context(), "clip")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Seek(-16, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	before := f.gets
	f.mu.Unlock()
	if before != 0 {
		t.Fatal("opening/seeking downloaded the clip")
	}
	tail, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(tail, data[len(data)-16:]) {
		t.Fatal("tail range failed", err)
	}
	f.mu.Lock()
	bytesRead := f.bytesRead
	f.mu.Unlock()
	if bytesRead != 16 {
		t.Fatalf("seeking fetched %d bytes, want 16", bytesRead)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(all, data) {
		t.Fatal("multi-range stream failed", err)
	}
	f.mu.Lock()
	f.badRange = true
	f.mu.Unlock()
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(make([]byte, 1)); err == nil {
		t.Fatal("accepted invalid range response")
	}
	f.mu.Lock()
	f.deny = true
	f.mu.Unlock()
	if _, err := s.Stat(t.Context(), "clip"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("access denied mistaken for missing: %v", err)
	}
}

func TestS3MultipartCommitAndAbort(t *testing.T) {
	s, fixture := testS3(t)
	f, err := os.CreateTemp(t.TempDir(), "large")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const size = 65 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Save(t.Context(), "large", f); err != nil || n != size {
		t.Fatal(n, err)
	}
	if n, err := s.Stat(t.Context(), "large"); err != nil || n != size {
		t.Fatal(n, err)
	}
	fixture.mu.Lock()
	fixture.failPart = true
	fixture.mu.Unlock()
	if err := s.multipart(t.Context(), "library/failed", f, size); err == nil {
		t.Fatal("part error swallowed")
	}
	fixture.mu.Lock()
	aborts := fixture.aborts
	fixture.mu.Unlock()
	if aborts != 1 {
		t.Fatalf("failed multipart left upload active: %d aborts", aborts)
	}
}

func TestDiskReadRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	disk, err := NewDisk(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir() + "/secret"
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root+"/link"); err != nil {
		t.Fatal(err)
	}
	if _, err := disk.Open(t.Context(), "link"); err == nil {
		t.Fatal("read escaped object root")
	}
}
