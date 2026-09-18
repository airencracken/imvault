// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"imvault/internal/apikeys"
	"imvault/internal/models"
	"imvault/internal/store"
)

// maxKeyLifetime bounds an optional key expiry, in days.
const maxKeyLifetimeDays = 3650

// handleAPIKeysPage lists the signed-in user's API keys.
func (s *Server) handleAPIKeysPage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	keys, err := s.store.APIKeysByUser(r.Context(), user.ID)
	if err != nil {
		s.log.Error("api keys page: list", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	s.renderPage(w, http.StatusOK, "api_keys", apiKeysView{
		base:    s.base(r, "API keys"),
		Keys:    keys,
		BaseURL: s.absoluteURL(r, ""),
	})
}

// handleAPIKeyCreate mints a key and returns the panel with the key shown once.
func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "Untitled key"
	}
	name = models.Truncate(name, 64)

	var expiresAt *time.Time
	if days, err := strconv.Atoi(strings.TrimSpace(r.FormValue("expires_days"))); err == nil && days > 0 {
		if days > maxKeyLifetimeDays {
			days = maxKeyLifetimeDays
		}
		e := time.Now().UTC().AddDate(0, 0, days)
		expiresAt = &e
	}

	generated := apikeys.Generate()
	if _, err := s.store.CreateAPIKey(r.Context(), user.ID, name, generated.Prefix, generated.Hash, expiresAt); err != nil {
		s.log.Error("create api key", "error", err)
		s.renderAPIKeysPanel(w, r, "", "Could not create the key.")
		return
	}

	s.renderAPIKeysPanel(w, r, generated.Full, "")
}

// handleAPIKeyDelete revokes a key.
func (s *Server) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid key", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteAPIKey(r.Context(), id, user.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderAPIKeysPanel(w, r, "", "That key no longer exists.")
			return
		}
		s.log.Error("delete api key", "id", id, "error", err)
		s.renderAPIKeysPanel(w, r, "", "Could not revoke the key.")
		return
	}

	s.renderAPIKeysPanel(w, r, "", "")
}

// renderAPIKeysPanel re-renders the key list, optionally revealing a new key.
func (s *Server) renderAPIKeysPanel(w http.ResponseWriter, r *http.Request, newKey, message string) {
	user := currentUser(r.Context())

	keys, err := s.store.APIKeysByUser(r.Context(), user.ID)
	if err != nil {
		s.log.Error("api keys panel: list", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	s.renderPartial(w, "api_keys_panel", apiKeysView{
		base:    s.base(r, "API keys"),
		Keys:    keys,
		NewKey:  newKey,
		Error:   message,
		BaseURL: s.absoluteURL(r, ""),
	})
}
