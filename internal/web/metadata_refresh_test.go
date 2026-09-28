// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"imvault/internal/metadata"
	"imvault/internal/storage"
)

type metadataStorage struct {
	storage.Backend
	opens    atomic.Int32
	fail     bool
	failRead bool
}

func (s *metadataStorage) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	s.opens.Add(1)
	if s.fail {
		return nil, errors.New("temporary storage failure")
	}
	if s.failRead {
		return unreadableOriginal{}, nil
	}
	return s.Backend.Open(ctx, key)
}

type unreadableOriginal struct{}

func (unreadableOriginal) Read([]byte) (int, error)       { return 0, errors.New("temporary read failure") }
func (unreadableOriginal) Seek(int64, int) (int64, error) { return 0, nil }
func (unreadableOriginal) Close() error                   { return nil }

func gpsUploadFixture(t *testing.T, brokenEXIF bool) []byte {
	t.Helper()
	data, err := os.ReadFile("../metadata/testdata/gps-values-before-directory.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if brokenEXIF {
		start := bytes.Index(data, []byte("Exif\x00\x00")) + 6
		block := data[start:]
		root := int(binary.LittleEndian.Uint32(block[4:]))
		count := int(binary.LittleEndian.Uint16(block[root:]))
		for i := 0; i < count; i++ {
			entry := block[root+2+i*12:]
			if binary.LittleEndian.Uint16(entry) == 0x8769 {
				binary.LittleEndian.PutUint32(entry[8:], 0xfffffff0)
			}
		}
	}
	return data
}

func TestFreshUploadRecoversGPSDespiteBrokenCameraMetadata(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	key := h.seedKey(u.ID, "test", nil)
	resp, raw := h.apiUpload(key, map[string]string{"visibility": "public"}, map[string][]byte{"photo.jpg": gpsUploadFixture(t, true)})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("fresh upload: %d %s", resp.StatusCode, raw)
	}
	upload := decodeUpload(t, raw)
	if len(upload.Files) != 1 || upload.Files[0].Details.Location() != "51.50740, -0.12730 (35m)" {
		t.Fatalf("fresh upload lost GPS: %s", raw)
	}
	_, page := h.sessionFor(t, u.ID).get("/f/" + upload.Files[0].ID)
	section := strings.Index(page, `class="panel stack file-location"`)
	collapsed := strings.Index(page, `<details class="panel stack file-details">`)
	if collapsed < 0 {
		collapsed = len(page)
	}
	if section < 0 || section > collapsed || !strings.Contains(page[section:collapsed], "51.50740, -0.12730") {
		t.Fatal("location is absent or hidden behind a disclosure")
	}
	_, page = h.newSession(t).get("/f/" + upload.Files[0].ID)
	if strings.Contains(page, "51.5074") || strings.Contains(page, "openstreetmap.org") || !strings.Contains(page, "Location is hidden") {
		t.Fatal("public location policy was not preserved")
	}
}

func TestLegacyDetailsRefreshOncePerOriginal(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	key := h.seedKey(u.ID, "test", nil)
	data := gpsUploadFixture(t, false)
	var ids []string
	for i := 0; i < 2; i++ {
		_, raw := h.apiUpload(key, map[string]string{"visibility": "public", "metadata": "hidden"}, map[string][]byte{"photo.jpg": data})
		ids = append(ids, decodeUpload(t, raw).Files[0].ID)
	}
	file, err := h.store.FileByID(t.Context(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DB().Exec(`UPDATE blobs SET details_json = '{"camera":"Cached Camera","artist":"Retained Artist"}', details_version = 0 WHERE sha256 = ?`, file.SHA256); err != nil {
		t.Fatal(err)
	}
	objects := &metadataStorage{Backend: h.srv.objects}
	h.srv.objects = objects
	// An anonymous page visit can refresh the cache, but must still hide GPS.
	_, page := h.newSession(t).get("/f/" + ids[0])
	if strings.Contains(page, "51.5074") || !strings.Contains(page, "Location is hidden") {
		t.Fatal("refresh leaked GPS to an anonymous viewer")
	}
	for _, id := range ids {
		_, raw := h.apiJSON(http.MethodGet, "/api/v1/files/"+id, key, nil)
		if !strings.Contains(string(raw), "51.5074") || !strings.Contains(string(raw), "Retained Artist") {
			t.Fatalf("refresh missing GPS or erased existing fields: %s", raw)
		}
		fresh, err := h.store.FileByID(t.Context(), id)
		if err != nil || fresh.DetailsVersion != metadata.Version || fresh.Metadata != file.Metadata || fresh.Visibility != file.Visibility {
			t.Fatalf("refresh changed policy or version: %+v (%v)", fresh, err)
		}
	}
	if objects.opens.Load() != 1 {
		t.Fatalf("re-read shared original %d times", objects.opens.Load())
	}
	original, err := objects.Backend.Open(t.Context(), file.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	stored, err := io.ReadAll(original)
	if err != nil || !bytes.Equal(data, stored) {
		t.Fatal("refresh changed original bytes")
	}
}

func TestMetadataRefreshFailuresAreRetryableAndEmptyIsCached(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser("alice")
	key := h.seedKey(u.ID, "test", nil)
	id := h.uploadOne(key, "plain.png", 32, 32)
	if _, err := h.store.DB().Exec(`UPDATE blobs SET details_version = 0`); err != nil {
		t.Fatal(err)
	}
	objects := &metadataStorage{Backend: h.srv.objects, fail: true}
	h.srv.objects = objects
	owner := h.sessionFor(t, u.ID)
	_, page := owner.get("/f/" + id)
	if !strings.Contains(page, "Location details could not be refreshed") {
		t.Fatal("storage failure was mistaken for absent GPS")
	}
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil || file.DetailsVersion != 0 {
		t.Fatal("failure marked metadata current")
	}
	objects.fail = false
	objects.failRead = true
	_, page = owner.get("/f/" + id)
	if !strings.Contains(page, "Location details could not be refreshed") {
		t.Fatal("read failure was mistaken for absent GPS")
	}
	objects.failRead = false
	_, page = owner.get("/f/" + id)
	if !strings.Contains(page, "No readable GPS coordinates were found") {
		t.Fatal("plain photo has no location explanation")
	}
	owner.get("/f/" + id)
	if objects.opens.Load() != 3 {
		t.Fatal("plain photo was parsed repeatedly")
	}
}
