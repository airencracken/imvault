// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/config"
	"imvault/internal/models"
	"imvault/internal/store"
)

// gpsMarker is the string the fixture hides in its Exif. It stands in for a
// coordinate: anything recognisable would do, and a test that looks for the
// real thing would have to parse Exif to check its own fixture.
const gpsMarker = "GPSLatitude=51.5074"

// jpegWithLocation is a small JPEG carrying an Exif segment with a location in
// it, which is what a phone produces.
func jpegWithLocation(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 12), G: uint8(y * 12), B: 40, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	base := buf.Bytes()

	payload := []byte("Exif\x00\x00MM\x00*\x00\x00\x00\x08" + gpsMarker + "GPSLongitude=-0.1276")
	length := len(payload) + 2
	segment := []byte{0xFF, 0xE1, byte(length >> 8), byte(length & 0xFF)}
	segment = append(segment, payload...)

	out := make([]byte, 0, len(base)+len(segment))
	out = append(out, base[:2]...)
	out = append(out, segment...)
	out = append(out, base[2:]...)
	return out
}

// uploadWith stores one fixture with the given form fields.
func uploadWith(t *testing.T, h *harness, fields map[string]string, data []byte) string {
	t.Helper()

	resp, body := h.uploadFiles(fields, []uploadFile{{name: "photo.jpg", data: data}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d (%s)", resp.StatusCode, truncate(body))
	}
	id := firstFileID(t, body)
	if id == "" {
		t.Fatalf("no file id in %s", truncate(body))
	}
	return id
}

// fetchRaw returns the body the server serves for a file's original.
func fetchRaw(t *testing.T, h *harness, id string) (int, []byte) {
	t.Helper()

	resp, body := h.get("/f/" + id + "/raw")
	return resp.StatusCode, []byte(body)
}

// storedOriginal reads the object the server kept, to prove it still has what
// was uploaded. A test that only checked the served bytes would pass even if
// the upload had silently dropped the metadata.
func storedOriginal(t *testing.T, h *harness, id string) []byte {
	t.Helper()

	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := h.store.BlobBySHA(t.Context(), file.SHA256)
	if err != nil {
		t.Fatal(err)
	}

	obj, err := h.srv.objects.Open(blob.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAPublicFileIsServedWithoutItsMetadata(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	data := jpegWithLocation(t)
	id := uploadWith(t, h, map[string]string{"visibility": "public"}, data)

	// The fixture really does carry it, and the original is kept as uploaded:
	// the promise that originals are never re-encoded is still true.
	if !bytes.Contains(storedOriginal(t, h, id), []byte(gpsMarker)) {
		t.Fatal("the stored original lost its metadata; the upload is at fault, not the serving")
	}

	status, served := fetchRaw(t, h, id)
	if status != http.StatusOK {
		t.Fatalf("public raw = %d, want 200", status)
	}
	if bytes.Contains(served, []byte(gpsMarker)) {
		t.Error("a public file served its location")
	}
	if bytes.Contains(served, []byte("Exif")) {
		t.Error("a public file served an Exif segment")
	}
	// And it is still the same picture.
	if _, err := jpeg.Decode(bytes.NewReader(served)); err != nil {
		t.Errorf("the stripped file is not a decodable JPEG: %v", err)
	}
}

func TestAMembersFileKeepsItsMetadata(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	id := uploadWith(t, h, map[string]string{"visibility": "members"}, jpegWithLocation(t))

	status, served := fetchRaw(t, h, id)
	if status != http.StatusOK {
		t.Fatalf("members raw = %d, want 200", status)
	}
	// The group is the audience the file was shared with, so the date and the
	// camera are part of what they were given.
	if !bytes.Contains(served, []byte(gpsMarker)) {
		t.Error("a members-only file lost its metadata")
	}
}

func TestAnExplicitSettingOverridesVisibilityBothWays(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	// Hidden on a members-only file, which visibility would have allowed.
	hidden := uploadWith(t, h, map[string]string{
		"visibility": "members",
		"metadata":   "hidden",
	}, jpegWithLocation(t))

	if _, served := fetchRaw(t, h, hidden); bytes.Contains(served, []byte(gpsMarker)) {
		t.Error("an explicit hidden setting was ignored")
	}

	// Shown on a public file. This is the escape hatch, and it has to work: it
	// is how somebody accepts the exposure for a file whose metadata could not
	// otherwise be removed.
	shown := uploadWith(t, h, map[string]string{
		"visibility": "public",
		"metadata":   "shown",
	}, jpegWithLocation(t))

	if _, served := fetchRaw(t, h, shown); !bytes.Contains(served, []byte(gpsMarker)) {
		t.Error("an explicit shown setting was ignored")
	}
}

func TestAnAlbumCanHideMetadataButNeverRevealIt(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	// A members-only file, which on its own would keep its metadata.
	hidden := uploadWith(t, h, map[string]string{"visibility": "members"}, jpegWithLocation(t))
	// A file that says to hide it, for the other direction.
	alreadyHidden := uploadWith(t, h, map[string]string{
		"visibility": "members",
		"metadata":   "hidden",
	}, jpegWithLocation(t))

	h.sharedAlbum(t, "Trip", models.VisibilityMembers, models.AlbumAccessOwner)
	trip, err := h.store.AlbumBySlug(t.Context(), "trip")
	if err != nil {
		t.Fatal(err)
	}

	// The album asks for metadata to be hidden.
	if err := h.store.UpdateAlbum(t.Context(), trip.ID, store.AlbumInput{
		Title:      "Trip",
		Visibility: models.VisibilityMembers,
		Access:     models.AlbumAccessOwner,
		Metadata:   models.MetadataHidden,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{hidden, alreadyHidden} {
		if err := h.store.AddFileToAlbum(t.Context(), trip.ID, id); err != nil {
			t.Fatal(err)
		}
	}

	if _, served := fetchRaw(t, h, hidden); bytes.Contains(served, []byte(gpsMarker)) {
		t.Error("an album did not tighten a file's metadata")
	}

	// Now the album says to show it. That must not loosen the file that
	// explicitly hid its own, because the album's owner and the file's owner
	// are not always the same person.
	if err := h.store.UpdateAlbum(t.Context(), trip.ID, store.AlbumInput{
		Title:      "Trip",
		Visibility: models.VisibilityMembers,
		Access:     models.AlbumAccessOwner,
		Metadata:   models.MetadataShown,
	}); err != nil {
		t.Fatal(err)
	}

	if _, served := fetchRaw(t, h, alreadyHidden); bytes.Contains(served, []byte(gpsMarker)) {
		t.Error("an album revealed metadata a file had hidden")
	}
}

func TestMetadataThatCannotBeRemovedIsRefusedRatherThanServed(t *testing.T) {
	// No ffmpeg, so a public clip's metadata cannot be removed. The honest
	// answer is to refuse, not to serve it as though it were clean.
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.FFmpegPath = "ffmpeg-that-does-not-exist"
		cfg.FFprobePath = "ffprobe-that-does-not-exist"
	})
	h.provisionAdmin("boss")

	clip := makeWebM(t)
	resp, body := h.uploadFiles(map[string]string{"visibility": "public"}, []uploadFile{
		{name: "clip.webm", data: clip},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d (%s)", resp.StatusCode, truncate(body))
	}
	id := firstFileID(t, body)

	status, served := fetchRaw(t, h, id)
	if status == http.StatusOK {
		t.Fatalf("a clip whose metadata could not be removed was served anyway (%d bytes)", len(served))
	}
	if status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", status)
	}
	if !bytes.Contains(served, []byte("not being served")) {
		t.Errorf("the refusal does not explain itself: %s", truncate(string(served)))
	}
	// The escape hatch has to be named, or a person is stuck.
	if !bytes.Contains(served, []byte("Shown")) {
		t.Errorf("the refusal does not say how to proceed: %s", truncate(string(served)))
	}

	// And setting it to shown does serve it, which is the point of refusing:
	// it forces a decision rather than making one silently.
	resp, _ = h.postForm("/f/"+id+"/metadata", url.Values{
		"csrf_token": {h.csrf()},
		"metadata":   {"shown"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("setting metadata = %d", resp.StatusCode)
	}
	if status, _ := fetchRaw(t, h, id); status != http.StatusOK {
		t.Errorf("after choosing to show the metadata, raw = %d, want 200", status)
	}
}

func TestTheCleanCopyIsSharedByContent(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	data := jpegWithLocation(t)
	first := uploadWith(t, h, map[string]string{"visibility": "public"}, data)
	second := uploadWith(t, h, map[string]string{"visibility": "public"}, data)

	// Two files, one piece of content, so one clean copy between them.
	file, err := h.store.FileByID(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	before := countStoredObjects(t, h.dataDir)

	if _, served := fetchRaw(t, h, second); bytes.Contains(served, []byte(gpsMarker)) {
		t.Fatal("the second file served its metadata")
	}

	blob, err := h.store.BlobBySHA(t.Context(), file.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if blob.CleanKey == "" {
		t.Fatal("no clean copy was recorded")
	}
	if after := countStoredObjects(t, h.dataDir); after != before+1 {
		t.Errorf("objects went from %d to %d, want one clean copy added", before, after)
	}

	// Both files point at the same copy.
	other, err := h.store.FileByID(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	otherBlob, err := h.store.BlobBySHA(t.Context(), other.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if otherBlob.CleanKey != blob.CleanKey {
		t.Errorf("the same content produced two clean copies: %q and %q",
			blob.CleanKey, otherBlob.CleanKey)
	}
}

func TestRemovingTheLastFileRemovesTheCleanCopy(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	id := uploadWith(t, h, map[string]string{"visibility": "public"}, jpegWithLocation(t))
	if _, served := fetchRaw(t, h, id); bytes.Contains(served, []byte(gpsMarker)) {
		t.Fatal("the file served its metadata")
	}
	if objects := countStoredObjects(t, h.dataDir); objects == 0 {
		t.Fatal("nothing was stored")
	}

	h.postForm("/f/"+id+"/delete", url.Values{"csrf_token": {h.csrf()}, "next": {"/gallery"}})

	// The clean copy belongs to the content, so it goes when the content does.
	// Leaving it behind would be a slow disk leak that nothing would notice.
	if objects := countStoredObjects(t, h.dataDir); objects != 0 {
		t.Errorf("%d objects remain after the last file went", objects)
	}
}

func TestTheAccountExportStillCarriesTheOriginal(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	data := jpegWithLocation(t)
	uploadWith(t, h, map[string]string{"visibility": "public"}, data)

	// The export is the owner's own data going to the owner, so it is the
	// original that belongs in it — not the copy made for strangers.
	resp, body := h.get("/settings/account/export")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export = %d, want 200", resp.StatusCode)
	}
	if !bytes.Contains([]byte(body), []byte(gpsMarker)) {
		t.Error("the export did not contain the original")
	}
	if strings.Contains(body, cleanMarker) {
		t.Error("the export contained the metadata-free copy")
	}
}
