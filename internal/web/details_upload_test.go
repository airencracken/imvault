// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestUploadedGPSReachesOwnerDetailsAndAPI(t *testing.T) {
	data, err := os.ReadFile("../metadata/testdata/gps-values-before-directory.jpg")
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	user := h.seedUser("alice")
	owner := h.sessionFor(t, user.ID)
	key := h.seedKey(user.ID, "test", nil)
	for i := 0; i < 2; i++ {
		resp, raw := h.apiUpload(key, map[string]string{"visibility": "public"}, map[string][]byte{"photo.jpg": data})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("upload = %d: %s", resp.StatusCode, raw)
		}
		uploaded := decodeUpload(t, raw)
		if len(uploaded.Files) != 1 {
			t.Fatalf("upload: %s", raw)
		}
		if details := uploaded.Files[0].Details; details == nil || details.Location() != "51.50740, -0.12730 (35m)" || details.Camera != "TestCam One" {
			t.Errorf("upload response lost EXIF: %s", raw)
		}
		path := "/f/" + uploaded.Files[0].ID
		_, page := owner.get(path)
		if !strings.Contains(page, "51.50740, -0.12730 (35m)") || !strings.Contains(page, "TestCam One") {
			t.Error("owner's photo details lost the uploaded EXIF")
		}
		_, page = h.newSession(t).get(path)
		if strings.Contains(page, "51.50740") || !strings.Contains(page, "TestCam One") {
			t.Error("public metadata policy did not hide only identifying details")
		}
		_, raw = h.apiJSON(http.MethodGet, "/api/v1/files/"+uploaded.Files[0].ID, key, nil)
		var file apiFileJSON
		if err := json.Unmarshal(raw, &file); err != nil || file.Details.Location() != "51.50740, -0.12730 (35m)" {
			t.Errorf("saved API details lost GPS: %s (%v)", raw, err)
		}
		// A re-upload must repair details cached by the old parser, even
		// though its original and renditions are reused.
		seedDetails(t, h, uploaded.Files[0].ID, `{"camera":"TestCam One"}`)
	}
}
