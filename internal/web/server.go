// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web implements the HTTP surface: pages, HTMX fragments and the
// binary image endpoints.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"imvault/internal/config"
	"imvault/internal/mail"
	"imvault/internal/media"
	"imvault/internal/oidc"
	"imvault/internal/ratelimit"
	"imvault/internal/secrets"
	"imvault/internal/storage"
	"imvault/internal/store"
)

//go:embed templates
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// Server holds the application dependencies and the routing table.
type Server struct {
	cfg     *config.Config
	store   *store.Store
	objects storage.Backend
	media   *media.Processor
	mail    mail.Sender
	secrets *secrets.Cipher
	log     *slog.Logger
	render  *renderer
	// oidc is the optional identity provider. It resolves on first use, so an
	// issuer that is briefly unreachable does not need a restart.
	oidc    *oidc.Lazy
	uploads *ratelimit.Limiter
	logins  *ratelimit.Limiter
	// settings holds the instance policy, cached because it is read on nearly
	// every request and only ever written by the admin page.
	settings atomic.Pointer[settingSource]
	handler  http.Handler
	// mailRetryInterval is how often the outbound queue is swept.
	mailRetryInterval time.Duration
	// processing bounds how many uploads are worked on at once, across
	// everybody. Rate limiting is per identity and does not bound the total.
	processing *gate
	content    contentLocks
}

// New constructs a Server and installs the middleware chain.
func New(cfg *config.Config, st *store.Store, objects storage.Backend, proc *media.Processor, sender mail.Sender, cipher *secrets.Cipher, log *slog.Logger) (*Server, error) {
	r, err := newRenderer()
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:               cfg,
		store:             st,
		objects:           objects,
		media:             proc,
		mail:              sender,
		secrets:           cipher,
		log:               log,
		render:            r,
		uploads:           ratelimit.New(cfg.UploadRatePerHour, cfg.UploadBurst),
		logins:            ratelimit.New(cfg.LoginRatePerHour, cfg.LoginBurst),
		mailRetryInterval: cfg.MailRetryInterval,
		processing:        newGate(cfg.MaxConcurrentUploads),
		// The provider is not contacted here: discovery happens on first use,
		// so an issuer that is briefly unreachable does not stop the instance
		// from serving, and does not need a restart once it is back.
		oidc: oidc.NewLazy(oidc.Config{
			Issuer:         cfg.OIDCIssuer,
			ClientID:       cfg.OIDCClientID,
			ClientSecret:   cfg.OIDCClientSecret,
			Name:           cfg.OIDCName,
			Scopes:         cfg.OIDCScopes,
			AllowedDomains: cfg.OIDCAllowedDomains,
		}),
	}

	// Two-factor authentication cannot work without a key, so a wiring mistake
	// should stop the process rather than surface as a 500 the first time
	// somebody tries to enrol.
	if s.secrets == nil {
		return nil, errors.New("web: a secret key is required")
	}

	if err := s.installSettings(context.Background()); err != nil {
		return nil, err
	}

	mux := s.routes()

	// Order matters: session and API-key authentication run before the CSRF
	// check, because a request authenticated by a bearer token is not
	// vulnerable to CSRF and must be exempt.
	// The semicolon rewrite runs before the CSRF middleware, which parses the
	// form to find its token, and therefore before anything else can read a
	// field and find it missing.
	s.handler = s.recoverMW(s.logMW(s.sessionMW(s.apiAuthMW(s.requestLimitsMW(s.semicolonMW(s.csrfMW(mux)))))))
	return s, nil
}

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// StartCleanup runs the retention reaper until ctx is cancelled. It is safe to
// call in a goroutine.
func (s *Server) StartCleanup(ctx context.Context) {
	s.cleanupLoop(ctx)
}

// StartWorkers runs every background loop until ctx is cancelled.
func (s *Server) StartWorkers(ctx context.Context) {
	go s.StartCleanup(ctx)
	go s.StartMailRetry(ctx)
}

// routes builds the ServeMux. Patterns use Go 1.22 method-and-wildcard syntax.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	staticSub, err := fsSub(staticFS, "static")
	if err != nil {
		panic(fmt.Sprintf("web: static assets: %v", err))
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", immutableCache(http.FileServer(http.FS(staticSub)))))
	mux.HandleFunc("GET /branding/{name}", s.handleBrandingAsset)

	mux.HandleFunc("GET /healthz", s.handleHealth)

	// Authentication
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.rateLimitLogins(s.handleLogin))
	mux.HandleFunc("GET /login/2fa", s.handleLoginTwoFactorPage)
	mux.HandleFunc("POST /login/2fa", s.rateLimitLogins(s.handleLoginTwoFactor))
	mux.HandleFunc("GET /register", s.handleRegisterPage)
	mux.HandleFunc("GET /auth/oidc/start", s.handleOIDCStart)
	mux.HandleFunc("GET /auth/oidc/callback", s.handleOIDCCallback)
	mux.HandleFunc("GET /auth/oidc/complete", s.handleOIDCComplete)
	mux.HandleFunc("POST /auth/oidc/complete", s.handleOIDCCompleteSubmit)
	mux.HandleFunc("POST /register", s.handleRegister)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /forgot", s.handleForgotPage)
	mux.HandleFunc("POST /forgot", s.handleForgot)
	mux.HandleFunc("GET /reset/{token}", s.handleResetPage)
	mux.HandleFunc("POST /reset/{token}", s.handleReset)
	mux.HandleFunc("GET /verify/{token}", s.handleVerifyEmail)

	// Library
	mux.HandleFunc("GET /gallery", s.requireUser(s.handleGallery))
	mux.HandleFunc("GET /favorites", s.requireUser(s.handleFavorites))
	mux.HandleFunc("GET /recent", s.requireUser(s.handleRecent))
	mux.HandleFunc("GET /upload", s.handleUploadPage)
	mux.HandleFunc("POST /upload", s.rateLimitUploads(s.handleUpload))

	// Individual files
	mux.HandleFunc("GET /f/{id}", s.handleFilePage)
	mux.HandleFunc("GET /f/{id}/raw", s.handleFileRaw)
	mux.HandleFunc("GET /f/{id}/thumb", s.handleFileThumb)
	mux.HandleFunc("GET /f/{id}/preview", s.handleFilePreview)
	mux.HandleFunc("POST /f/{id}/favorite", s.requireUser(s.handleFavorite))
	mux.HandleFunc("POST /f/{id}/rename", s.requireUser(s.handleFileRename))
	mux.HandleFunc("POST /f/{id}/description", s.requireUser(s.handleFileDescription))
	mux.HandleFunc("POST /f/{id}/visibility", s.requireUser(s.handleFileVisibility))
	mux.HandleFunc("POST /f/{id}/metadata", s.requireUser(s.handleFileMetadata))
	mux.HandleFunc("POST /f/{id}/delete", s.requireUser(s.handleFileDelete))
	mux.HandleFunc("POST /f/{id}/tags", s.requireUser(s.handleTagAdd))
	mux.HandleFunc("POST /f/{id}/tags/{tagID}/delete", s.requireUser(s.handleTagRemove))

	// Albums
	mux.HandleFunc("POST /reports", s.requireUser(s.handleReport))
	mux.HandleFunc("GET /moderation", s.requireModerator(s.handleModeration))
	mux.HandleFunc("GET /moderation/log", s.requireModerator(s.handleModerationLog))
	mux.HandleFunc("POST /moderation/reports/{id}/resolve", s.requireModerator(s.handleResolveReport))
	mux.HandleFunc("GET /albums", s.requireUser(s.handleAlbumsPage))
	mux.HandleFunc("POST /albums", s.requireUser(s.handleAlbumCreate))
	mux.HandleFunc("GET /a/{slug}", s.handleAlbumPage)
	mux.HandleFunc("POST /a/{slug}/delete", s.requireUser(s.handleAlbumDelete))
	mux.HandleFunc("POST /a/{slug}/settings", s.requireUser(s.handleAlbumUpdate))
	mux.HandleFunc("POST /a/{slug}/files", s.requireUser(s.handleAlbumAddFiles))
	mux.HandleFunc("POST /a/{slug}/files/{fileID}/delete", s.requireUser(s.handleAlbumRemoveFile))

	// Tags
	mux.HandleFunc("GET /tags", s.handleTagsPage)
	mux.HandleFunc("GET /tags/{username}/{slug}", s.handleTagPage)

	// Share links
	mux.HandleFunc("GET /p/{id}", s.handleShortLink)

	// Account settings
	mux.HandleFunc("GET /settings/account", s.requireUser(s.handleAccountPage))
	mux.HandleFunc("GET /settings/account/export", s.requireUser(s.handleAccountExport))
	mux.HandleFunc("POST /settings/account/delete", s.requireUser(s.handleAccountDelete))
	mux.HandleFunc("POST /settings/account/identities/{id}/delete", s.requireUser(s.handleUnlinkIdentity))
	mux.HandleFunc("GET /settings/2fa", s.requireUser(s.handleTwoFactorPage))
	mux.HandleFunc("GET /settings/2fa/qr", s.requireUser(s.handleTwoFactorQR))
	mux.HandleFunc("POST /settings/2fa/begin", s.requireUser(s.handleTwoFactorBegin))
	mux.HandleFunc("POST /settings/2fa/confirm", s.requireUser(s.handleTwoFactorConfirm))
	mux.HandleFunc("POST /settings/2fa/disable", s.requireUser(s.handleTwoFactorDisable))
	mux.HandleFunc("POST /settings/2fa/recovery", s.requireUser(s.handleTwoFactorRecovery))
	mux.HandleFunc("GET /settings/password", s.requireUser(s.handleChangePasswordPage))
	mux.HandleFunc("POST /settings/password", s.requireUser(s.handleChangePassword))
	mux.HandleFunc("POST /settings/email", s.requireUser(s.handleChangeEmail))
	mux.HandleFunc("GET /settings/api-keys", s.requireUser(s.handleAPIKeysPage))
	mux.HandleFunc("POST /settings/api-keys", s.requireUser(s.handleAPIKeyCreate))
	mux.HandleFunc("POST /settings/api-keys/{id}/delete", s.requireUser(s.handleAPIKeyDelete))

	// Admin. Everything here is instance-wide, so it is administrator-only.
	mux.HandleFunc("GET /admin", s.requireAdmin(s.handleAdminDashboard))
	mux.HandleFunc("GET /admin/users", s.requireAdmin(s.handleAdminUsers))
	mux.HandleFunc("POST /admin/users/{id}/limits", s.requireAdmin(s.handleAdminSetLimits))
	mux.HandleFunc("POST /admin/users/{id}/disabled", s.requireAdmin(s.handleAdminSetDisabled))
	mux.HandleFunc("POST /admin/users/{id}/role", s.requireAdmin(s.handleAdminSetRole))
	mux.HandleFunc("POST /admin/users/{id}/invites", s.requireAdmin(s.handleAdminSetInvitePermission))
	mux.HandleFunc("POST /admin/users/{id}/delete", s.requireAdmin(s.handleAdminDeleteUser))
	mux.HandleFunc("POST /admin/users/{id}/reset", s.requireAdmin(s.handleAdminIssueReset))
	mux.HandleFunc("POST /admin/users/{id}/2fa", s.requireAdmin(s.handleAdminClearTwoFactor))
	mux.HandleFunc("GET /admin/mail", s.requireAdmin(s.handleAdminMail))
	mux.HandleFunc("POST /admin/mail/{id}/retry", s.requireAdmin(s.handleAdminRetryMail))
	mux.HandleFunc("POST /admin/mail/{id}/delete", s.requireAdmin(s.handleAdminDeleteMail))
	mux.HandleFunc("POST /admin/maintenance/storage", s.requireAdmin(s.handleAdminRecomputeStorage))
	mux.HandleFunc("POST /admin/maintenance/blobs", s.requireAdmin(s.handleAdminRecomputeBlobs))
	mux.HandleFunc("GET /admin/invites", s.requireAdmin(s.handleAdminInvites))
	mux.HandleFunc("POST /admin/invites", s.requireAdmin(s.handleAdminCreateInvite))
	mux.HandleFunc("POST /admin/invites/{id}/revoke", s.requireAdmin(s.handleAdminRevokeInvite))
	mux.HandleFunc("GET /invites", s.requireInviter(s.handleUserInvites))
	mux.HandleFunc("POST /invites", s.requireInviter(s.handleAdminCreateInvite))
	mux.HandleFunc("POST /invites/{id}/revoke", s.requireInviter(s.handleUserRevokeInvite))
	mux.HandleFunc("GET /admin/settings", s.requireAdmin(s.handleAdminSettings))
	mux.HandleFunc("POST /admin/settings", s.requireAdmin(s.handleAdminSaveSettings))
	mux.HandleFunc("POST /admin/settings/branding-assets", s.requireAdmin(s.handleAdminSaveBrandingAssets))
	mux.HandleFunc("POST /admin/settings/clear", s.requireAdmin(s.handleAdminClearSettings))
	mux.HandleFunc("GET /admin/files", s.requireModerator(s.handleAdminFiles))
	mux.HandleFunc("POST /admin/files/{id}/delete", s.requireModerator(s.handleAdminDeleteFile))

	// Programmatic API. These authenticate with a bearer token rather than a
	// cookie, so they are exempt from CSRF and accept no session fallback.
	mux.HandleFunc("GET /api/v1/me", s.requireAPIKey(s.apiMe))

	mux.HandleFunc("POST /api/v1/upload", s.requireAPIKey(s.rateLimitUploads(s.apiUpload)))

	mux.HandleFunc("GET /api/v1/files", s.requireAPIKey(s.apiListFiles))
	mux.HandleFunc("GET /api/v1/files/{id}", s.requireAPIKey(s.apiFile))
	mux.HandleFunc("PATCH /api/v1/files/{id}", s.requireAPIKey(s.apiPatchFile))
	mux.HandleFunc("DELETE /api/v1/files/{id}", s.requireAPIKey(s.apiDeleteFile))

	mux.HandleFunc("POST /api/v1/files/{id}/tags", s.requireAPIKey(s.apiAddFileTag))
	mux.HandleFunc("DELETE /api/v1/files/{id}/tags/{tagRef}", s.requireAPIKey(s.apiRemoveFileTag))

	mux.HandleFunc("GET /api/v1/albums", s.requireAPIKey(s.apiListAlbums))
	mux.HandleFunc("POST /api/v1/albums", s.requireAPIKey(s.apiCreateAlbum))
	mux.HandleFunc("GET /api/v1/albums/{ref}", s.requireAPIKey(s.apiGetAlbum))
	mux.HandleFunc("PATCH /api/v1/albums/{ref}", s.requireAPIKey(s.apiPatchAlbum))
	mux.HandleFunc("DELETE /api/v1/albums/{ref}", s.requireAPIKey(s.apiDeleteAlbum))
	mux.HandleFunc("POST /api/v1/albums/{ref}/files", s.requireAPIKey(s.apiAddAlbumFiles))
	mux.HandleFunc("DELETE /api/v1/albums/{ref}/files/{fileID}", s.requireAPIKey(s.apiRemoveAlbumFile))

	mux.HandleFunc("GET /api/v1/tags", s.requireAPIKey(s.apiListTags))

	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB().PingContext(r.Context()); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok"))
}
