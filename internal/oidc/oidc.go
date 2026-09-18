// SPDX-License-Identifier: AGPL-3.0-or-later

// Package oidc wraps OpenID Connect: discovery, the authorization code flow
// with PKCE, and verification of the identity token.
//
// Verification is delegated to github.com/coreos/go-oidc rather than done here.
// Signature checking against a rotating key set has too many ways to be subtly
// wrong — algorithm confusion, key selection, clock skew — and a login path is
// the worst place to discover one.
package oidc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"imvault/internal/ids"
)

// Config describes the provider an instance trusts.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// Name is what the button says. Defaults to "Single sign-on".
	Name string
	// Scopes defaults to openid, profile, and email.
	Scopes []string
	// AllowedDomains, when set, restricts which email domains may sign in. It
	// is the control a family on one mail domain or a company wants.
	AllowedDomains []string
}

// Configured reports whether enough was given to attempt a sign-in at all.
func (c Config) Configured() bool {
	return strings.TrimSpace(c.Issuer) != "" && strings.TrimSpace(c.ClientID) != ""
}

// DisplayName is what the interface calls this provider.
func (c Config) DisplayName() string {
	if name := strings.TrimSpace(c.Name); name != "" {
		return name
	}
	return "Single sign-on"
}

// Identity is what the provider asserted about somebody.
type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Username      string
	Name          string
}

// Attempt is one sign-in in progress: what has to survive the round trip to the
// provider and back.
//
// The verifier never leaves this server; the browser carries only the state.
type Attempt struct {
	URL       string
	State     string
	Nonce     string
	Verifier  string
	CreatedAt time.Time
}

// Expired reports whether an attempt is too old to still be in progress.
func (a Attempt) Expired(now time.Time, window time.Duration) bool {
	return now.Sub(a.CreatedAt) > window
}

// Provider is a discovered issuer.
type Provider struct {
	cfg   Config
	oidc  *oidc.Provider
	oauth oauth2.Config
}

// New discovers the issuer. That is a network round trip, which is why callers
// normally hold a Lazy rather than one of these.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	if !cfg.Configured() {
		return nil, errors.New("oidc: no issuer or client id is configured")
	}

	discovered, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery against %s failed: %w", cfg.Issuer, err)
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}

	return &Provider{
		cfg:  cfg,
		oidc: discovered,
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     discovered.Endpoint(),
			Scopes:       scopes,
		},
	}, nil
}

// Start begins a sign-in, returning where to send the browser and what to
// remember until it comes back.
func (p *Provider) Start(redirectURL string) Attempt {
	attempt := Attempt{
		State:     ids.Token(24),
		Nonce:     ids.Token(24),
		Verifier:  oauth2.GenerateVerifier(),
		CreatedAt: time.Now().UTC(),
	}

	conf := p.oauth
	conf.RedirectURL = redirectURL

	// The nonce ties the token that comes back to this attempt, and PKCE stops
	// a code intercepted on the way from being redeemed by somebody else.
	attempt.URL = conf.AuthCodeURL(attempt.State,
		oidc.Nonce(attempt.Nonce),
		oauth2.S256ChallengeOption(attempt.Verifier))

	return attempt
}

// Exchange redeems the code and verifies what comes back.
func (p *Provider) Exchange(ctx context.Context, code, verifier, redirectURL, nonce string) (*Identity, error) {
	conf := p.oauth
	conf.RedirectURL = redirectURL

	token, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("oidc: exchanging the code failed: %w", err)
	}

	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		return nil, errors.New("oidc: the provider returned no identity token")
	}

	verified, err := p.oidc.Verifier(&oidc.Config{ClientID: p.cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("oidc: the identity token did not verify: %w", err)
	}

	// Verify checks the signature, the issuer, the audience, and the expiry. It
	// does not check the nonce, because only the caller knows which attempt
	// this was supposed to answer.
	if verified.Nonce != nonce {
		return nil, errors.New("oidc: the identity token belongs to a different sign-in")
	}

	var claims struct {
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
	}
	if err := verified.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oidc: reading the claims failed: %w", err)
	}

	identity := &Identity{
		Subject:       verified.Subject,
		Email:         strings.TrimSpace(claims.Email),
		EmailVerified: claims.EmailVerified,
		Username:      strings.TrimSpace(claims.PreferredUsername),
		Name:          strings.TrimSpace(claims.Name),
	}
	if identity.Username == "" {
		identity.Username = strings.TrimSpace(claims.Email)
	}
	if identity.Username == "" {
		identity.Username = identity.Subject
	}

	if err := p.allows(identity); err != nil {
		return nil, err
	}
	return identity, nil
}

// allows enforces the domain allowlist, when one is set.
func (p *Provider) allows(identity *Identity) error {
	if len(p.cfg.AllowedDomains) == 0 {
		return nil
	}

	domain := emailDomain(identity.Email)
	if domain == "" {
		return errors.New("oidc: the provider did not supply an email address")
	}
	for _, allowed := range p.cfg.AllowedDomains {
		if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(allowed), "@"), domain) {
			return nil
		}
	}
	return fmt.Errorf("oidc: %s is not one of this instance's allowed domains", domain)
}

func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return email[at+1:]
}

// Lazy resolves a provider on first use.
//
// Discovery is a network round trip, and doing it at startup would mean an
// issuer that is briefly down stops the instance from running at all, and needs
// a restart before its own login works again. Resolving on demand keeps the
// failure where it belongs: in the sign-in that needed it.
type Lazy struct {
	cfg Config

	mu     sync.Mutex
	cached *Provider
}

// NewLazy wraps a configuration without contacting anything.
func NewLazy(cfg Config) *Lazy { return &Lazy{cfg: cfg} }

// Enabled reports whether an issuer is configured. It does not prove the issuer
// is reachable.
func (l *Lazy) Enabled() bool { return l != nil && l.cfg.Configured() }

// Name is what the interface calls the provider, or empty when there is none
// configured. Templates key the button off it, so an unconfigured provider must
// not fall back to a display name.
func (l *Lazy) Name() string {
	if l == nil || !l.cfg.Configured() {
		return ""
	}
	return l.cfg.DisplayName()
}

// Get returns the discovered provider, discovering it once.
func (l *Lazy) Get(ctx context.Context) (*Provider, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.cached != nil {
		return l.cached, nil
	}
	provider, err := New(ctx, l.cfg)
	if err != nil {
		return nil, err
	}
	l.cached = provider
	return l.cached, nil
}
