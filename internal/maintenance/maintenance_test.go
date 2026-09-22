// SPDX-License-Identifier: AGPL-3.0-or-later

package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/media"
	"imvault/internal/models"
	"imvault/internal/secrets"
	"imvault/internal/storage"
	"imvault/internal/store"
)

type fixture struct {
	cfg     *config.Config
	store   *store.Store
	objects *storage.Disk
	file    *models.File
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir, DBPath: filepath.Join(dir, "imvault.db"), SecretKeyFile: filepath.Join(dir, "secret.key")}
	database, err := db.Open(t.Context(), cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	st := store.New(database)
	objects, err := storage.NewDisk(filepath.Join(dir, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secrets.Load(cfg.SecretKeyFile, "")
	if err != nil {
		t.Fatal(err)
	}
	user, err := st.CreateUser(t.Context(), store.NewUser{Username: "alice", PasswordHash: "preserved-password-hash", Role: models.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := cipher.Encrypt("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE users SET totp_secret = ?, totp_enabled = 1 WHERE id = ?`, secret, user.ID); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(encoded.Bytes())
	sha := hex.EncodeToString(hash[:])
	key := "orig/" + sha + ".png"
	if _, err := objects.Save(t.Context(), key, bytes.NewReader(encoded.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Save(t.Context(), "thumb/old.png", strings.NewReader("old thumbnail")); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureBlob(t.Context(), sha, int64(encoded.Len()), key, "thumb/old.png", "", ""); err != nil {
		t.Fatal(err)
	}
	file := &models.File{ID: "photo", UserID: &user.ID, OriginalName: "family.png", Description: "A day out.", Ext: "png", Mime: "image/png", Kind: models.KindImage, Size: int64(encoded.Len()), SHA256: sha, Visibility: models.VisibilityPrivate, Metadata: models.MetadataHidden, CreatedAt: time.Now()}
	if err := st.CreateFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	return fixture{cfg, st, objects, file}
}

func TestBackupRestorePreservesMediaAccountsAndSecrets(t *testing.T) {
	f := newFixture(t)
	backup := filepath.Join(t.TempDir(), "snapshot")
	if err := Backup(t.Context(), f.cfg, f.store, f.objects, backup, io.Discard); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored")
	if err := Restore(t.Context(), backup, destination, io.Discard); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(t.Context(), filepath.Join(destination, "imvault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	restored, err := store.New(database).FileByID(t.Context(), f.file.ID)
	if err != nil || restored.Description != f.file.Description || restored.Visibility != models.VisibilityPrivate || restored.Metadata != models.MetadataHidden {
		t.Fatalf("file changed after restore: %#v %v", restored, err)
	}
	objects, err := storage.NewDisk(filepath.Join(destination, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t.Context(), objects, Object{restored.ObjectKey, f.file.Size, f.file.SHA256}); err != nil {
		t.Fatal(err)
	}
	var password, encrypted string
	if err := database.QueryRow(`SELECT password_hash, totp_secret FROM users WHERE username='alice'`).Scan(&password, &encrypted); err != nil {
		t.Fatal(err)
	}
	cipher, err := secrets.Load(filepath.Join(destination, "secret.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.Decrypt(encrypted)
	if err != nil || plain != "JBSWY3DPEHPK3PXP" || password != "preserved-password-hash" {
		t.Fatal("credentials changed during restore")
	}
	info, err := os.Stat(filepath.Join(destination, "secret.key"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("restored secret permissions are unsafe")
	}
	if err := Restore(t.Context(), backup, destination, io.Discard); err == nil {
		t.Fatal("restore overwrote an existing instance")
	}
	if err := Backup(t.Context(), f.cfg, f.store, f.objects, backup, io.Discard); err == nil {
		t.Fatal("backup overwrote a snapshot")
	}
}

func TestBackupRejectsMissingMediaAndWrongSecret(t *testing.T) {
	f := newFixture(t)
	output := filepath.Join(t.TempDir(), "backup")
	f.cfg.SecretKey = strings.Repeat("ab", 32)
	if err := Backup(t.Context(), f.cfg, f.store, f.objects, output, io.Discard); err == nil {
		t.Fatal("backup accepted the wrong encryption key")
	}
	f.cfg.SecretKey = ""
	if err := f.objects.Delete(t.Context(), "thumb/old.png"); err != nil {
		t.Fatal(err)
	}
	if err := Backup(t.Context(), f.cfg, f.store, f.objects, output, io.Discard); err == nil {
		t.Fatal("missing rendition silently omitted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed backup was published")
	}
}

func TestRestoreRejectsTamperingAndManifestTraversal(t *testing.T) {
	f := newFixture(t)
	backup := filepath.Join(t.TempDir(), "snapshot")
	if err := Backup(t.Context(), f.cfg, f.store, f.objects, backup, io.Discard); err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(backup)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(backup, "objects", manifest.Objects[0].Key)
	if err := os.WriteFile(file, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored")
	if err := Restore(t.Context(), backup, destination, io.Discard); err == nil {
		t.Fatal("corrupt object restored")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("failed restore was published")
	}
	manifest.Objects[0].Key = "../../escape"
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Restore(t.Context(), backup, destination, io.Discard); err == nil {
		t.Fatal("path traversal accepted")
	}
}

type failCopy struct {
	storage.Backend
	remaining int
	writes    int
}

func (f *failCopy) Save(ctx context.Context, key string, src io.Reader) (int64, error) {
	if f.remaining == 0 {
		return 0, errors.New("simulated interruption")
	}
	f.remaining--
	f.writes++
	return f.Backend.Save(ctx, key, src)
}

func TestMigrationResumesAndDoesNotOverwriteDifferentObjects(t *testing.T) {
	f := newFixture(t)
	disk, err := storage.NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dest := &failCopy{Backend: disk, remaining: 1}
	if err := Migrate(t.Context(), f.store, f.objects, dest, io.Discard); err == nil {
		t.Fatal("interruption swallowed")
	}
	dest.remaining = 10
	if err := Migrate(t.Context(), f.store, f.objects, dest, io.Discard); err != nil {
		t.Fatal(err)
	}
	if dest.writes != 2 {
		t.Fatalf("resume copied already verified objects: %d writes", dest.writes)
	}
	if _, err := disk.Save(t.Context(), "thumb/old.png", strings.NewReader("unrelated")); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), f.store, f.objects, dest, io.Discard); err == nil {
		t.Fatal("migration overwrote different target content")
	}
	if dest.writes != 2 {
		t.Fatal("conflicting destination was written")
	}
	entries, err := Inventory(t.Context(), f.store)
	if err != nil || len(entries) != 2 {
		t.Fatal("source records changed")
	}
}

func TestRegenerationPreservesOriginalsAndChangesCachedURLs(t *testing.T) {
	f := newFixture(t)
	before, err := f.store.FileByID(t.Context(), f.file.ID)
	if err != nil {
		t.Fatal(err)
	}
	processor := media.NewProcessor(8, 16, 82, 0, nil)
	if err := Regenerate(t.Context(), f.store, f.objects, processor, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.FileByID(t.Context(), f.file.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.ThumbURL() == after.ThumbURL() || after.PreviewKey == "" {
		t.Fatal("regenerated renditions kept stale browser URLs")
	}
	if after.OriginalName != before.OriginalName || after.Description != before.Description || after.Visibility != before.Visibility || after.Metadata != before.Metadata || after.SHA256 != before.SHA256 {
		t.Fatal("regeneration changed file settings")
	}
	if err := verify(t.Context(), f.objects, Object{after.ObjectKey, after.Size, after.SHA256}); err != nil {
		t.Fatal(err)
	}
	r, err := f.objects.Open(t.Context(), after.ThumbKey)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	img, _, err := image.Decode(r)
	if err != nil || img.Bounds().Dx() != 8 || img.Bounds().Dy() != 4 {
		t.Fatal("wrong regenerated dimensions", err)
	}
	if err := Regenerate(t.Context(), f.store, f.objects, processor, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	again, err := f.store.FileByID(t.Context(), f.file.ID)
	if err != nil || again.ThumbURL() != after.ThumbURL() {
		t.Fatal("identical regeneration changed content URLs")
	}
}
