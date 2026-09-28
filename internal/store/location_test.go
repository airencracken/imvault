// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"

	"imvault/internal/models"
)

func TestLocationPolicyIsIndependentAndAlbumsOnlyRestrict(t *testing.T) {
	s, ctx := newTestStore(t)
	u := mustUser(t, s, ctx, "alice")
	f := mustFile(t, s, ctx, "photo", &u.ID, models.VisibilityPublic, nil)
	a, err := s.CreateAlbum(ctx, u.ID, AlbumInput{Title: "Album", Visibility: models.VisibilityMembers})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddFileToAlbum(ctx, a.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	for _, fileEXIF := range models.MetadataLevels() {
		for _, fileGPS := range models.MetadataLevels() {
			f.Metadata, f.Location = fileEXIF, fileGPS
			for _, albumEXIF := range models.MetadataLevels() {
				for _, albumGPS := range models.MetadataLevels() {
					if err := s.UpdateAlbum(ctx, a.ID, AlbumInput{Title: "Album", Metadata: albumEXIF, Location: albumGPS}); err != nil {
						t.Fatal(err)
					}
					got, err := s.EffectiveMetadataPolicies(ctx, f)
					if err != nil {
						t.Fatal(err)
					}
					if fileEXIF.Resolve(f.Visibility) == models.MetadataHidden && got.Metadata != models.MetadataHidden {
						t.Fatal("album revealed EXIF")
					}
					if fileGPS == models.MetadataHidden && got.Location != models.MetadataHidden {
						t.Fatal("album revealed GPS")
					}
					if albumGPS == models.MetadataHidden && got.Location != models.MetadataHidden {
						t.Fatal("album GPS restriction ignored")
					}
					if fileGPS == models.MetadataShown && albumGPS == models.MetadataShown && got.Location != models.MetadataShown {
						t.Fatal("EXIF setting overrode explicit GPS sharing")
					}
					if fileGPS == models.MetadataInherit && albumGPS == models.MetadataInherit && got.Location != got.Metadata {
						t.Fatal("legacy behavior changed")
					}
				}
			}
		}
	}
}

func TestLocationUpdateAtomicityAndBlobKeys(t *testing.T) {
	s, ctx := newTestStore(t)
	u := mustUser(t, s, ctx, "alice")
	f := mustFile(t, s, ctx, "photo", &u.ID, models.VisibilityPrivate, nil)
	shown, hidden := models.MetadataShown, models.MetadataHidden
	if err := s.UpdateFile(ctx, f.ID, FileUpdate{Metadata: &shown, Location: &hidden}); err != nil {
		t.Fatal(err)
	}
	bad := models.MetadataPolicy("invalid")
	if err := s.UpdateFile(ctx, f.ID, FileUpdate{Metadata: &hidden, Location: &bad}); err == nil {
		t.Fatal("invalid location accepted")
	}
	f, err := s.FileByID(ctx, f.ID)
	if err != nil || f.Metadata != shown || f.Location != hidden {
		t.Fatal("invalid update partially committed")
	}
	for _, camera := range []bool{true, false} {
		if err := s.SetBlobFilteredKey(ctx, f.SHA256, filteredTestKey(camera), camera); err != nil {
			t.Fatal(err)
		}
	}
	blob, err := s.BlobBySHA(ctx, f.SHA256)
	if err != nil || len(blob.Keys()) != 5 {
		t.Fatalf("filtered keys omitted: %+v %v", blob, err)
	}
	if err := s.ClearBlobCleanKey(ctx, f.SHA256); err != nil {
		t.Fatal(err)
	}
	blob, err = s.BlobBySHA(ctx, f.SHA256)
	if err != nil || blob.CameraKey != "" || blob.LocationKey != "" {
		t.Fatal("cache invalidation missed filtered variants")
	}
}

func filteredTestKey(camera bool) string {
	if camera {
		return "camera.jpg"
	}
	return "location.jpg"
}
