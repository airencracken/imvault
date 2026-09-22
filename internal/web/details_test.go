// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strings"
	"testing"

	"imvault/internal/models"
	"imvault/internal/store"
)

// storedDetails is what the upload path would have written, set directly here.
//
// Parsing is tested where it lives, in the metadata package, against a real
// Exif block. This file is about what the page does with the result, and
// building a second Exif fixture to prove the same parser works would test the
// parser again and the page not at all.
const storedDetails = `{"taken":"21 September 2026 at 14:30","camera":"TestCam One",` +
	`"lens":"TestLens 50mm","exposure":"1/250","aperture":"f/2.8","iso":"200",` +
	`"focal":"50mm","software":"imvault test suite","artist":"A Photographer",` +
	`"latitude":51.5074,"longitude":-0.1273,"altitude":35}`

// seedDetails attaches parsed details to whatever content a file holds.
func seedDetails(t *testing.T, h *harness, fileID, details string) {
	t.Helper()

	file, err := h.store.FileByID(t.Context(), fileID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DB().ExecContext(t.Context(),
		`UPDATE blobs SET details_json = ? WHERE sha256 = ?`, details, file.SHA256); err != nil {
		t.Fatal(err)
	}
}

// uploadPlain stores an ordinary image with no metadata of its own.
func uploadPlain(t *testing.T, h *harness, fields map[string]string) string {
	t.Helper()

	resp, body := h.uploadFiles(fields, []uploadFile{
		{name: "photo.jpg", data: pngFixture(t, 32, 32)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d (%s)", resp.StatusCode, truncate(body))
	}
	return firstFileID(t, body)
}

func TestTheOwnerSeesTheWholeDetail(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	// A public file, so the policy resolves to hidden for everybody else.
	id := uploadPlain(t, h, map[string]string{"visibility": "public"})
	seedDetails(t, h, id, storedDetails)

	_, page := h.get("/f/" + id)

	if !strings.Contains(page, "Photo details") {
		t.Fatal("the page has no details section")
	}
	// Collapsed, because a wall of camera settings above the picture is noise.
	if !strings.Contains(page, `<details class="panel stack file-details">`) ||
		strings.Contains(page, `class="panel stack file-details" open`) {
		t.Error("the details are not collapsed by default")
	}
	for _, want := range []string{"TestCam One", "21 September 2026 at 14:30", "51.50740, -0.12730"} {
		if !strings.Contains(page, want) {
			t.Errorf("the owner cannot see %q in their own photograph", want)
		}
	}
}

func TestAPublicPhotoHidesWhereItWasTakenFromEverybodyElse(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	other := h.seedUser("other")

	id := uploadPlain(t, h, map[string]string{"visibility": "public"})
	seedDetails(t, h, id, storedDetails)

	viewer := h.sessionFor(t, other.ID)
	resp, page := viewer.get("/f/" + id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a member cannot open a public file: %d", resp.StatusCode)
	}

	// What a family archive is for survives: when it was taken, and with what.
	for _, want := range []string{"TestCam One", "21 September 2026 at 14:30", "1/250", "200"} {
		if !strings.Contains(page, want) {
			t.Errorf("%q is missing from the details", want)
		}
	}

	// Where it was taken, and who by, do not.
	for _, secret := range []string{"51.50740", "-0.12730", "A Photographer"} {
		if strings.Contains(page, secret) {
			t.Errorf("a public photograph told a stranger %q", secret)
		}
	}

	// And the page says so, rather than looking like a file that never had any.
	if !strings.Contains(page, "not shown while this file is not") {
		t.Error("the page does not say that something is being withheld")
	}
}

func TestShowingMetadataShowsItToEverybodyWhoCanSeeTheFile(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	other := h.seedUser("other")

	id := uploadPlain(t, h, map[string]string{
		"visibility": "public",
		"metadata":   "shown",
	})
	seedDetails(t, h, id, storedDetails)

	_, page := h.sessionFor(t, other.ID).get("/f/" + id)

	if !strings.Contains(page, "51.50740, -0.12730") {
		t.Error("a file set to show its metadata hid the location")
	}
	if strings.Contains(page, "not shown while this file is not") {
		t.Error("the page claims to be withholding something")
	}
}

func TestAnAlbumCanHideWhatAFileWouldShow(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	other := h.seedUser("other")

	// A members-only file, which on its own would show everything to a member.
	id := uploadPlain(t, h, map[string]string{"visibility": "members"})
	seedDetails(t, h, id, storedDetails)

	viewer := h.sessionFor(t, other.ID)
	if _, page := viewer.get("/f/" + id); !strings.Contains(page, "51.50740") {
		t.Fatal("a members-only file did not show its location to a member")
	}

	h.sharedAlbum(t, "Trip", models.VisibilityMembers, models.AlbumAccessOwner)
	trip, err := h.store.AlbumBySlug(t.Context(), "trip")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.UpdateAlbum(t.Context(), trip.ID, store.AlbumInput{
		Title:      "Trip",
		Visibility: models.VisibilityMembers,
		Access:     models.AlbumAccessOwner,
		Metadata:   models.MetadataHidden,
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.AddFileToAlbum(t.Context(), trip.ID, id); err != nil {
		t.Fatal(err)
	}

	if _, page := viewer.get("/f/" + id); strings.Contains(page, "51.50740") {
		t.Error("an album did not hide the location of a file in it")
	}
}

func TestAFileWithNothingToSayHasNoSection(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	id := uploadPlain(t, h, map[string]string{"visibility": "members"})
	_, page := h.get("/f/" + id)

	if strings.Contains(page, "Photo details") {
		t.Error("a file with no metadata grew a details section")
	}
}

func TestTheAPIReportsDetailsToTheirOwner(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("alice")
	key := h.seedKey(user.ID, "laptop", nil)

	resp, raw := h.apiUpload(key, map[string]string{"visibility": "public"}, map[string][]byte{
		"photo.jpg": pngFixture(t, 32, 32),
	})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d (%s)", resp.StatusCode, truncate(string(raw)))
	}
	uploaded := decodeUpload(t, raw)
	if len(uploaded.Files) != 1 {
		t.Fatalf("upload failed: %s", truncate(string(raw)))
	}
	seedDetails(t, h, uploaded.Files[0].ID, storedDetails)

	_, raw = h.apiJSON(http.MethodGet, "/api/v1/files/"+uploaded.Files[0].ID, key, nil)

	// The API serves an account its own files, so nothing is withheld: masking
	// somebody's data from themselves in their own tool helps nobody.
	for _, want := range []string{"TestCam One", "51.5074", "A Photographer"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the API withheld %q from the file's owner", want)
		}
	}
}

func TestDetailsSurviveAReloadOfThePage(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")

	id := uploadPlain(t, h, map[string]string{"visibility": "members"})
	seedDetails(t, h, id, storedDetails)

	// The details come from the blob through the file listing, so a second read
	// is a real check that the join is wired rather than a cached string.
	_, first := h.get("/f/" + id)
	_, second := h.get("/f/" + id)
	if !strings.Contains(first, "TestCam One") || !strings.Contains(second, "TestCam One") {
		t.Error("the details did not survive a second read")
	}
}

// TestTheFamilySeesWhereThePhotographWasTaken names the case the whole feature
// exists for.
//
// Nothing is configured here: this is the default. A file shared with a group
// shows that group its coordinates, while the same coordinates stay out of what
// the public is served and shown. The answer is not "strip it and show
// nothing", because where the photograph was taken is much of what there is to
// share — the axis is the audience, not the field.
func TestTheFamilySeesWhereThePhotographWasTaken(t *testing.T) {
	h := newHarness(t)
	h.provisionAdmin("boss")
	family := h.seedUser("family")

	shared := uploadPlain(t, h, map[string]string{"visibility": "members"})
	seedDetails(t, h, shared, storedDetails)

	// The group sees it, coordinates and all, and is not told anything is
	// being withheld, because nothing is.
	_, page := h.sessionFor(t, family.ID).get("/f/" + shared)
	if !strings.Contains(page, "51.50740, -0.12730") {
		t.Error("the group was not shown where the photograph was taken")
	}
	if strings.Contains(page, "not shown while this file is not") {
		t.Error("the group is told something is withheld, and nothing is")
	}

	// The public does not, and keeps the parts a stranger can do nothing with.
	public := uploadPlain(t, h, map[string]string{"visibility": "public"})
	seedDetails(t, h, public, storedDetails)

	_, page = h.newSession(t).get("/f/" + public)
	if strings.Contains(page, "51.50740") {
		t.Error("a stranger was shown where a public photograph was taken")
	}
	if !strings.Contains(page, "21 September 2026 at 14:30") {
		t.Error("a stranger lost the date as well, which is not the point")
	}
	if !strings.Contains(page, "TestCam One") {
		t.Error("a stranger lost the camera as well, which is not the point")
	}
}
