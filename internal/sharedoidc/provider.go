// SPDX-License-Identifier: AGPL-3.0-or-later
// Synchronized from Comfylib's suite adapter; see scripts/suite-assets.json.
package sharedoidc

import (
	"context"
	"database/sql"
	"errors"
	oidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"imvault/internal/suite"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type provider struct {
	config suite.AuthConfig
	mu     sync.Mutex
	issuer *oidc.Provider
	client *http.Client
}

func FromEnvironment(prefix, baseURL string, db *sql.DB, hooks suite.AuthHooks) (*suite.Auth, error) {
	c := suite.AuthConfig{App: strings.ToLower(strings.TrimSuffix(prefix, "_")), Issuer: os.Getenv(prefix + "SHARED_ISSUER"), ClientID: os.Getenv(prefix + "SHARED_CLIENT_ID"), ClientSecret: os.Getenv(prefix + "SHARED_CLIENT_SECRET"), BaseURL: baseURL}
	if c.Issuer == "" && c.ClientID == "" && c.ClientSecret == "" {
		return nil, nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 10 * time.Second
	transport.MaxResponseHeaderBytes = 64 << 10
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return suite.NewAuth(c, &provider{config: c, client: client}, db, hooks)
}
func (p *provider) discovery(ctx context.Context) (*oidc.Provider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.issuer != nil {
		return p.issuer, nil
	}
	issuer, err := oidc.NewProvider(oidc.ClientContext(ctx, p.client), p.config.Issuer)
	if err == nil {
		p.issuer = issuer
	}
	return issuer, err
}
func (p *provider) oauth(issuer *oidc.Provider) oauth2.Config {
	return oauth2.Config{ClientID: p.config.ClientID, ClientSecret: p.config.ClientSecret, Endpoint: issuer.Endpoint(), RedirectURL: strings.TrimRight(p.config.BaseURL, "/") + "/auth/shared/callback", Scopes: []string{oidc.ScopeOpenID}}
}
func (p *provider) Authorize(ctx context.Context, state, nonce, verifier string) (string, error) {
	issuer, err := p.discovery(ctx)
	if err != nil {
		return "", err
	}
	config := p.oauth(issuer)
	return config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}
func (p *provider) Exchange(ctx context.Context, code, nonce, verifier string) (suite.Identity, error) {
	issuer, err := p.discovery(ctx)
	if err != nil {
		return suite.Identity{}, err
	}
	config := p.oauth(issuer)
	token, err := config.Exchange(oidc.ClientContext(ctx, p.client), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return suite.Identity{}, err
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return suite.Identity{}, errors.New("provider did not return an ID token")
	}
	identity, err := issuer.Verifier(&oidc.Config{ClientID: p.config.ClientID}).Verify(oidc.ClientContext(ctx, p.client), raw)
	if err != nil || identity.Nonce != nonce {
		return suite.Identity{}, errors.New("ID token verification failed")
	}
	return suite.Identity{Issuer: identity.Issuer, Subject: identity.Subject}, nil
}
