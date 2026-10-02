// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"imvault/internal/config"
	"imvault/internal/models"
)

// getWith performs a request as a particular client and returns the status and
// body. The body is read and closed, so the caller does not have to.
func getWith(t *testing.T, client *http.Client, target string) (int, string, http.Header) {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer mustClose(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	return resp.StatusCode, string(body), resp.Header
}

func TestMapURLSubstitution(t *testing.T) {
	got := mapURL("https://maps.example/{lat},{lon}@{zoom}?k={key}", "a b&c", 12, 51.5074, -0.1273)
	want := "https://maps.example/51.507400,-0.127300@12?k=a+b%26c"
	if got != want {
		t.Fatalf("mapURL = %q, want %q", got, want)
	}
}

// guestClient is a client with no session at all: h.client is signed in as the
// administrator once provisionAdmin has run.
func guestClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// mapHarness builds a harness with a fake static-map provider and one public
// upload carrying a location.
func mapHarness(t *testing.T, requested *[]string, providerStatus int) (*harness, *models.User, string) {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requested = append(*requested, r.URL.String())
		if providerStatus != http.StatusOK {
			w.WriteHeader(providerStatus)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		if _, err := w.Write(pngFixture(t, 20, 20)); err != nil {
			t.Errorf("fake provider write: %v", err)
		}
	}))
	t.Cleanup(provider.Close)

	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.MapURL = provider.URL + "/static?lat={lat}&lon={lon}&z={zoom}&key={key}"
		cfg.MapKey = "sekret key"
		cfg.MapZoom = 12
	})
	boss := h.provisionAdmin("boss")
	id := uploadWith(t, h, map[string]string{"visibility": "public"}, pngFixture(t, 40, 40))
	seedDetails(t, h, id, storedDetails)
	return h, boss, id
}

func TestTheLocationMapFollowsTheMetadataPolicy(t *testing.T) {
	var requested []string
	h, boss, id := mapHarness(t, &requested, http.StatusOK)
	owner := h.signIn(t, boss.ID)
	path := h.server.URL + "/f/" + id + "/map"

	// A public file hides its location, so a visitor gets no map at all.
	status, _, _ := getWith(t, guestClient(), path)
	if status != http.StatusNotFound {
		t.Fatalf("guest map = %d, want 404", status)
	}

	// The administrator may see the location, so the map is served and the
	// provider receives the substituted coordinates.
	status, _, header := getWith(t, owner, path)
	if status != http.StatusOK || header.Get("Content-Type") != "image/png" {
		t.Fatalf("owner map = %d (%s)", status, header.Get("Content-Type"))
	}
	if len(requested) != 1 {
		t.Fatalf("provider requests = %d, want 1: %v", len(requested), requested)
	}
	for _, want := range []string{"lat=51.507400", "lon=-0.127300", "z=12", "key=sekret+key"} {
		if !strings.Contains(requested[0], want) {
			t.Errorf("provider URL %q is missing %q", requested[0], want)
		}
	}

	// Showing the metadata makes the location visible, and the map with it.
	if err := h.store.SetFileMetadata(t.Context(), id, models.MetadataShown); err != nil {
		t.Fatal(err)
	}
	status, _, _ = getWith(t, guestClient(), path)
	if status != http.StatusOK {
		t.Fatalf("guest map after showing metadata = %d, want 200", status)
	}
}

func TestTheLocationMapIsOffUnlessConfigured(t *testing.T) {
	h := newHarness(t)
	boss := h.provisionAdmin("boss")
	owner := h.signIn(t, boss.ID)
	id := uploadWith(t, h, map[string]string{"visibility": "private"}, pngFixture(t, 40, 40))
	seedDetails(t, h, id, storedDetails)

	status, _, _ := getWith(t, owner, h.server.URL+"/f/"+id+"/map")
	if status != http.StatusNotFound {
		t.Fatalf("map with no configuration = %d, want 404", status)
	}
}

func TestTheFilePageOffersTheLocationOnlyWhenVisible(t *testing.T) {
	var requested []string
	h, boss, id := mapHarness(t, &requested, http.StatusOK)
	owner := h.signIn(t, boss.ID)
	page := h.server.URL + "/f/" + id

	_, body, _ := getWith(t, owner, page)
	if !strings.Contains(body, "/f/"+id+"/map") || !strings.Contains(body, "openstreetmap.org") {
		t.Fatal("the owner's page does not offer the map or the OSM link")
	}

	// The public file withholds the location from a visitor, so neither the map
	// nor the coordinates link should appear.
	_, body, _ = getWith(t, guestClient(), page)
	if strings.Contains(body, "/f/"+id+"/map") || strings.Contains(body, "openstreetmap.org") {
		t.Fatal("a withheld location still offered a map")
	}
}

func TestAMapProviderFailureIsReported(t *testing.T) {
	var requested []string
	h, boss, id := mapHarness(t, &requested, http.StatusInternalServerError)
	owner := h.signIn(t, boss.ID)

	status, _, _ := getWith(t, owner, h.server.URL+"/f/"+id+"/map")
	if status != http.StatusBadGateway {
		t.Fatalf("map with a failing provider = %d, want 502", status)
	}
}
