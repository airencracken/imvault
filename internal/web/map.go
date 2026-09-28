// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"imvault/internal/metadata"
	"imvault/internal/models"
)

// maxMapBytes bounds what a map provider can make the server hold.
const maxMapBytes = 5 << 20

// mapCacheSize bounds how many maps are kept. Coordinates are stable, so an
// entry never expires; the whole cache is dropped when it fills.
const mapCacheSize = 64

type mapImage struct {
	data        []byte
	contentType string
}

// mapZoom keeps a stray zoom from producing a nonsense provider URL or OSM
// link on an instance whose configuration was built by hand rather than loaded.
func mapZoom(zoom int) int {
	if zoom < 1 || zoom > 20 {
		return 13
	}
	return zoom
}

// mapURL substitutes a location into the operator's template. The template and
// key are configuration, never user input, and only the rounded coordinates,
// the zoom, and the key are inserted.
func mapURL(template, key string, zoom int, lat, lon float64) string {
	return strings.NewReplacer(
		"{lat}", strconv.FormatFloat(lat, 'f', 6, 64),
		"{lon}", strconv.FormatFloat(lon, 'f', 6, 64),
		"{zoom}", strconv.Itoa(zoom),
		"{key}", url.QueryEscape(key),
	).Replace(template)
}

// osmLink is the keyless fallback: the coordinates open on openstreetmap.org.
func osmLink(lat, lon float64, zoom int) string {
	return fmt.Sprintf("https://www.openstreetmap.org/?mlat=%.5f&mlon=%.5f#map=%d/%.5f/%.5f",
		lat, lon, zoom, lat, lon)
}

// handleFileMap proxies one static map. The provider key stays on the server,
// and the response is gated by the same metadata policy as the location itself,
// so a withheld coordinate yields no map rather than a blank one.
func (s *Server) handleFileMap(w http.ResponseWriter, r *http.Request) {
	if s.cfg.MapURL == "" {
		http.NotFound(w, r)
		return
	}
	file, ok := s.lookupVisibleFile(w, r)
	if !ok {
		return
	}
	details := metadata.DecodeDetails(file.Details)
	if details == nil || details.Latitude == nil || details.Longitude == nil {
		http.NotFound(w, r)
		return
	}
	if !s.locationVisible(r.Context(), file, currentUser(r.Context())) {
		http.NotFound(w, r)
		return
	}

	image, err := s.fetchMap(r.Context(), *details.Latitude, *details.Longitude)
	if err != nil {
		s.log.Error("map fetch", "id", file.ID, "error", err)
		http.Error(w, "The map is unavailable right now.", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", image.contentType)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodHead {
		_, _ = w.Write(image.data)
	}
}

// locationVisible answers whether this viewer may see where the photo was
// taken, which is the same question the details panel answers.
func (s *Server) locationVisible(ctx context.Context, file *models.File, viewer *models.User) bool {
	if canChangeFile(viewer, file) {
		return true
	}
	policies, err := s.store.EffectiveMetadataPolicies(ctx, file)
	if err != nil {
		s.log.Error("metadata policy", "id", file.ID, "error", err)
		return false
	}
	return policies.Location == models.MetadataShown
}

func (s *Server) fetchMap(ctx context.Context, lat, lon float64) (mapImage, error) {
	target := mapURL(s.cfg.MapURL, s.cfg.MapKey, mapZoom(s.cfg.MapZoom), lat, lon)
	if image, ok := s.cachedMap(target); ok {
		return image, nil
	}

	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return mapImage{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return mapImage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return mapImage{}, fmt.Errorf("map provider returned %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		return mapImage{}, fmt.Errorf("map provider returned %q", contentType)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMapBytes+1))
	if err != nil {
		return mapImage{}, err
	}
	if len(data) > maxMapBytes {
		return mapImage{}, fmt.Errorf("map image is larger than %d bytes", maxMapBytes)
	}
	image := mapImage{data: data, contentType: contentType}
	s.storeMap(target, image)
	return image, nil
}

func (s *Server) cachedMap(key string) (mapImage, bool) {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	image, ok := s.mapCache[key]
	return image, ok
}

func (s *Server) storeMap(key string, image mapImage) {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapCache == nil || len(s.mapCache) >= mapCacheSize {
		s.mapCache = make(map[string]mapImage, mapCacheSize)
	}
	s.mapCache[key] = image
}
