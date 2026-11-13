package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPublicUploadDefaultsWithoutJavaScript(t *testing.T) {
	h := newHarness(t)
	owner := h.sessionFor(t, h.seedUser("alice").ID)
	resp, page := owner.get("/upload")
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.StatusCode)
	}
	// A browser without JavaScript keeps every checkbox's rendered state
	// when the reader changes the visibility radio to Public.
	values := url.Values{"visibility": {"public"}}
	for _, field := range []string{"metadata", "location"} {
		values[field] = []string{"hidden"}
		marker := `type="checkbox" name="` + field + `"`
		_, after, found := strings.Cut(page, marker)
		if !found {
			t.Fatalf("missing %s control", field)
		}
		attributes, _, _ := strings.Cut(after, ">")
		if strings.Contains(attributes, "checked") {
			values[field] = append(values[field], "shown")
		}
	}
	resp, result := uploadValues(t, owner, values)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: %d %s", resp.StatusCode, result)
	}
	file, err := h.store.FileByID(t.Context(), firstFileID(t, result))
	if err != nil {
		t.Fatal(err)
	}
	if file.ShowCamera || file.ShowLocation {
		t.Fatalf("changing only visibility to Public shares default metadata: camera=%v location=%v", file.ShowCamera, file.ShowLocation)
	}
}
