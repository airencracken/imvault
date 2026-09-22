// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"imvault/internal/media"
	"imvault/internal/models"
	"imvault/internal/storage"
)

type remoteObjects struct{ storage.Backend }

func (remoteObjects) LocalPath(string) (string, bool) { return "", false }

func TestVideoPosterRepairUsesTemporaryFilesAndRetainsFailedRenditions(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg required")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe required")
	}
	clip := filepath.Join(t.TempDir(), "clip.webm")
	cmd := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=32x16:d=0.2", "-c:v", "libvpx", "-threads", "1", "-y", clip)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate clip: %s (%v)", out, err)
	}
	data, err := os.ReadFile(clip)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	key := "orig/" + sha + ".webm"
	if _, err := f.objects.Save(t.Context(), key, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.EnsureBlob(t.Context(), sha, int64(len(data)), key, "thumb/old.png", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateFile(t.Context(), &models.File{ID: "clip", UserID: f.file.UserID, OriginalName: "clip.webm", Ext: "webm", Mime: "video/webm", SHA256: sha, Size: int64(len(data)), Kind: models.KindVideo, Visibility: models.VisibilityPrivate}); err != nil {
		t.Fatal(err)
	}
	processor := media.NewProcessor(32, 64, 82, 0, nil)
	if err := Regenerate(t.Context(), f.store, f.objects, processor, true, io.Discard); err == nil {
		t.Fatal("missing ffmpeg reported as successful repair")
	}
	before, err := f.store.FileByID(t.Context(), "clip")
	if err != nil || before.ThumbKey != "thumb/old.png" {
		t.Fatal("failed regeneration replaced old rendition")
	}
	processor.Video = media.NewFFmpeg(ffmpeg, ffprobe)
	if err := Regenerate(t.Context(), f.store, remoteObjects{f.objects}, processor, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.FileByID(t.Context(), "clip")
	if err != nil || after.Width != 32 || after.Height != 16 || after.DurationMS <= 0 || before.ThumbURL() == after.ThumbURL() {
		t.Fatalf("clip not repaired: %#v (%v)", after, err)
	}
	image, err := f.store.FileByID(t.Context(), f.file.ID)
	if err != nil || image.ThumbKey != "thumb/old.png" {
		t.Fatal("videos-only changed a still image")
	}
}
