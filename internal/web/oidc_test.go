// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"imvault/internal/config"
	"imvault/internal/models"
)

// fakeIDP is a minimal OpenID Connect provider: discovery, a key set, and a
// token endpoint. Enough to exercise the real client against the real protocol
// rather than against a stub that agrees with it by construction.
type fakeIDP struct {
	server       *httptest.Server
	key          *rsa.PrivateKey
	kid          string
	clientID     string
	clientSecret string

	mu sync.Mutex
	// What the next identity token will assert.
	subject       string
	email         string
	emailVerified bool
	username      string
	nonce         string
	// What the token endpoint was actually sent, for assertions.
	lastVerifier  string
	lastRedirect  string
	lastChallenge string
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	idp := &fakeIDP{
		key:          key,
		kid:          "test-key",
		clientID:     "imvault-test",
		clientSecret: "shhh",
		subject:      "subject-1",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSONBody(t, w, map[string]any{
			"issuer":                                idp.server.URL,
			"authorization_endpoint":                idp.server.URL + "/authorize",
			"token_endpoint":                        idp.server.URL + "/token",
			"jwks_uri":                              idp.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := key.Public().(*rsa.PublicKey)
		writeJSONBody(t, w, map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": idp.kid,
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		// The tests read the parameters off the redirect instead of following
		// it, so this only has to exist.
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/token", idp.token)

	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (f *fakeIDP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	// Accept the client credentials either way: oauth2 tries basic auth first
	// and falls back to the form.
	clientID := r.FormValue("client_id")
	if id, _, ok := r.BasicAuth(); ok {
		clientID = id
	}
	if clientID != f.clientID {
		http.Error(w, "bad client", http.StatusUnauthorized)
		return
	}

	f.mu.Lock()
	f.lastVerifier = r.FormValue("code_verifier")
	f.lastRedirect = r.FormValue("redirect_uri")
	subject, email, verified, username, nonce := f.subject, f.email, f.emailVerified, f.username, f.nonce
	f.mu.Unlock()

	now := time.Now()
	claims := map[string]any{
		"iss":   f.server.URL,
		"sub":   subject,
		"aud":   f.clientID,
		"exp":   now.Add(5 * time.Minute).Unix(),
		"iat":   now.Unix(),
		"nonce": nonce,
	}
	if email != "" {
		claims["email"] = email
		claims["email_verified"] = verified
	}
	if username != "" {
		claims["preferred_username"] = username
	}

	writeJSONBody(nil, w, map[string]any{
		"access_token": "not-used",
		"token_type":   "Bearer",
		"expires_in":   300,
		"id_token":     f.sign(claims),
	})
}

// sign builds a compact RS256 JWT. Signing is the one thing a test provider has
// to do itself, and it is a dozen lines.
func (f *fakeIDP) sign(claims map[string]any) string {
	encode := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}

	signing := encode(map[string]any{"alg": "RS256", "typ": "JWT", "kid": f.kid}) +
		"." + encode(claims)

	sum := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// setIdentity is what the next token will assert.
func (f *fakeIDP) setIdentity(subject, email string, verified bool, username string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subject, f.email, f.emailVerified, f.username = subject, email, verified, username
}

func (f *fakeIDP) setNonce(nonce string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nonce = nonce
}

func (f *fakeIDP) setChallenge(challenge string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastChallenge = challenge
}

func (f *fakeIDP) exchange() (verifier, redirect, challenge string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastVerifier, f.lastRedirect, f.lastChallenge
}

// writeJSONBody writes JSON, tolerating a nil test handle for the httptest
// goroutine where no *testing.T is available.
func writeJSONBody(t *testing.T, w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	if err := enc.Encode(payload); err != nil && t != nil {
		t.Errorf("encode fake provider response: %v", err)
	}
}

// oidcHarness builds an instance that trusts the fake provider.
func oidcHarness(t *testing.T, idp *fakeIDP) *harness {
	t.Helper()

	return newHarnessWith(t, func(cfg *config.Config) {
		cfg.OIDCIssuer = idp.server.URL
		cfg.OIDCClientID = idp.clientID
		cfg.OIDCClientSecret = idp.clientSecret
		cfg.OIDCName = "Test ID"
	})
}

// authorize begins a sign-in and returns the URL the server chose, without
// following it.
func (h *harness) authorize(t *testing.T, client *http.Client, query string) *url.URL {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/auth/oidc/start"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("start = %d, want 303", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return location
}

// callback completes a sign-in, telling the fake provider which nonce and
// challenge the authorization request asked for.
func (h *harness) callback(t *testing.T, idp *fakeIDP, client *http.Client, authorizeURL *url.URL) *http.Response {
	t.Helper()

	idp.setNonce(authorizeURL.Query().Get("nonce"))
	idp.setChallenge(authorizeURL.Query().Get("code_challenge"))

	target := h.server.URL + "/auth/oidc/callback?code=a-code&state=" +
		url.QueryEscape(authorizeURL.Query().Get("state"))

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestOIDCRequestsAPKCEChallenge(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.get("/") // get a CSRF cookie and a session

	authorizeURL := h.authorize(t, h.client, "")

	if got := authorizeURL.Query().Get("client_id"); got != idp.clientID {
		t.Errorf("client_id = %q", got)
	}
	if got := authorizeURL.Query().Get("nonce"); got == "" {
		t.Error("the authorization request carries no nonce")
	}
	if got := authorizeURL.Query().Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if got := authorizeURL.Query().Get("code_challenge"); got == "" {
		t.Error("the authorization request carries no PKCE challenge")
	}

	// The verifier must never be in the browser's hands, only its hash.
	cookies := h.jar.Cookies(mustParse(t, h.server.URL+"/auth/oidc/callback"))
	var sealed string
	for _, cookie := range cookies {
		if cookie.Name == oidcStateCookie {
			sealed = cookie.Value
		}
	}
	if sealed == "" {
		t.Fatal("no sign-in cookie was set")
	}
	if strings.Contains(sealed, authorizeURL.Query().Get("state")) {
		t.Error("the state is readable in the cookie")
	}
	if strings.Contains(sealed, "verifier") {
		t.Error("the cookie looks unencrypted")
	}

	// The token request has to carry the verifier that hashes to the challenge
	// it advertised, or PKCE is decoration.
	resp := h.callback(t, idp, h.client, authorizeURL)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("callback = %d, want 303", resp.StatusCode)
	}

	verifier, redirect, challenge := idp.exchange()
	if verifier == "" {
		t.Fatal("the token request carried no code_verifier")
	}
	sum := sha256.Sum256([]byte(verifier))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != challenge {
		t.Error("the verifier does not match the challenge that was advertised")
	}
	if redirect == "" {
		t.Error("the token request carried no redirect_uri")
	}
}

func TestOIDCSignsInALinkedAccount(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.registerForm("boss")

	// An account already linked to this subject.
	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.LinkIdentity(t.Context(), boss.ID, idp.server.URL, "subject-1", "boss@example.com"); err != nil {
		t.Fatal(err)
	}

	idp.setIdentity("subject-1", "boss@example.com", true, "boss")

	// A fresh browser, so the sign-in is the only thing that could authenticate.
	client := h.newSession(t)
	authorizeURL := h.authorize(t, client.client, "")
	resp := h.callback(t, idp, client.client, authorizeURL)

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("callback = %d, want 303", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/recent" {
		t.Errorf("landed on %q, want /recent", got)
	}
	if resp, _ := client.get("/gallery"); resp.StatusCode != http.StatusOK {
		t.Errorf("the new session cannot reach the gallery: %d", resp.StatusCode)
	}
}

func TestOIDCLinksByAVerifiedAddress(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.registerForm("boss")

	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetEmail(t.Context(), boss.ID, "boss@example.com", false); err != nil {
		t.Fatal(err)
	}

	// The provider vouches for the address, so the account is found and linked.
	idp.setIdentity("subject-new", "boss@example.com", true, "boss")

	client := h.newSession(t)
	resp := h.callback(t, idp, client.client, h.authorize(t, client.client, ""))

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/recent" {
		t.Fatalf("callback = %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	identity, err := h.store.IdentityBySubject(t.Context(), idp.server.URL, "subject-new")
	if err != nil {
		t.Fatalf("the identity was not linked: %v", err)
	}
	if identity.UserID != boss.ID {
		t.Errorf("linked to account %d, want %d", identity.UserID, boss.ID)
	}
}

func TestOIDCDoesNotLinkAnUnverifiedAddress(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.registerForm("boss")

	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetEmail(t.Context(), boss.ID, "boss@example.com", false); err != nil {
		t.Fatal(err)
	}

	// The provider says it has not checked the address, so it is not evidence
	// of anything and must not hand over the account.
	idp.setIdentity("subject-sketchy", "boss@example.com", false, "boss")

	client := h.newSession(t)
	resp := h.callback(t, idp, client.client, h.authorize(t, client.client, ""))

	if resp.StatusCode != http.StatusSeeOther || !strings.HasSuffix(resp.Header.Get("Location"), "/auth/oidc/complete") {
		t.Fatalf("an unverified address skipped the extra step: %d -> %q",
			resp.StatusCode, resp.Header.Get("Location"))
	}
	if _, err := h.store.IdentityBySubject(t.Context(), idp.server.URL, "subject-sketchy"); err == nil {
		t.Error("an unverified address was linked anyway")
	}
	if resp, _ := client.get("/gallery"); resp.StatusCode == http.StatusOK {
		t.Error("an unverified address signed somebody in")
	}
}

func TestOIDCRefusesAnAmbiguousAddress(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.registerForm("first")

	// Two accounts, one address. Guessing which one is meant would be a way to
	// sign in as the wrong person.
	second := h.seedUser("second")
	if err := h.store.SetEmail(t.Context(), second.ID, "shared@example.com", true); err != nil {
		t.Fatal(err)
	}
	first, err := h.store.UserByUsername(t.Context(), "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetEmail(t.Context(), first.ID, "shared@example.com", true); err != nil {
		t.Fatal(err)
	}

	idp.setIdentity("subject-amb", "shared@example.com", true, "shared")

	client := h.newSession(t)
	resp := h.callback(t, idp, client.client, h.authorize(t, client.client, ""))

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an ambiguous address = %d, want a refusal", resp.StatusCode)
	}
	if _, err := h.store.IdentityBySubject(t.Context(), idp.server.URL, "subject-amb"); err == nil {
		t.Error("an ambiguous address was linked to somebody")
	}
}

func TestOIDCCompletionRespectsTheRegistrationPolicy(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.registerForm("boss")
	h.setPolicy(t, models.Settings{
		AllowSignup:           true,
		InviteOnly:            true,
		AllowAnonymousUploads: false,
		DefaultVisibility:     models.VisibilityMembers,
	})

	code, _ := h.seedInvite(t, 1, nil)
	idp.setIdentity("subject-friend", "friend@example.com", true, "friend")

	client := h.newSession(t)
	resp := h.callback(t, idp, client.client, h.authorize(t, client.client, ""))
	if !strings.HasSuffix(resp.Header.Get("Location"), "/auth/oidc/complete") {
		t.Fatalf("an unknown identity went to %q", resp.Header.Get("Location"))
	}

	// The page asks for a code, and the form refuses without one. A provider
	// must not be a second front door around an invitation-only instance.
	_, page := client.get("/auth/oidc/complete")
	if !strings.Contains(page, `name="invite"`) || !strings.Contains(page, "(required)") {
		t.Error("the completion page does not ask for an invitation")
	}

	resp, _ = client.post("/auth/oidc/complete", url.Values{"username": {"friend"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("completing without a code = %d, want 403", resp.StatusCode)
	}
	if _, err := h.store.UserByUsername(t.Context(), "friend"); err == nil {
		t.Fatal("an account was created without the invitation the instance requires")
	}

	// With one, it works, and the account and the link arrive together.
	resp, _ = client.post("/auth/oidc/complete", url.Values{
		"username": {"friend"},
		"invite":   {code},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("completing with a code = %d, want 303", resp.StatusCode)
	}

	created, err := h.store.UserByUsername(t.Context(), "friend")
	if err != nil {
		t.Fatalf("the account was not created: %v", err)
	}
	identity, err := h.store.IdentityBySubject(t.Context(), idp.server.URL, "subject-friend")
	if err != nil {
		t.Fatalf("the identity was not linked: %v", err)
	}
	if identity.UserID != created.ID {
		t.Errorf("linked to %d, want %d", identity.UserID, created.ID)
	}
	if !created.EmailVerified {
		t.Error("the verified address was not recorded as verified")
	}

	// And the single-use code was spent.
	invites, _, err := h.store.ListInvites(t.Context(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if invites[0].Uses != 1 {
		t.Errorf("invitation uses = %d, want 1", invites[0].Uses)
	}
}

func TestOIDCCompletionAsksForAUsernameOnAnOpenInstance(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.registerForm("boss")

	// The provider's idea of a username is a display name full of spaces.
	idp.setIdentity("subject-2", "new@example.com", true, "Ada Lovelace")

	client := h.newSession(t)
	resp := h.callback(t, idp, client.client, h.authorize(t, client.client, ""))
	if !strings.HasSuffix(resp.Header.Get("Location"), "/auth/oidc/complete") {
		t.Fatalf("went to %q", resp.Header.Get("Location"))
	}

	// The suggested name is already usable, and there is no code to give.
	resp, page := client.get("/auth/oidc/complete")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete page = %d", resp.StatusCode)
	}
	if !strings.Contains(page, `value="ada-lovelace"`) {
		t.Errorf("the suggested username is not usable: %s", truncate(page))
	}
	if strings.Contains(page, "(required)") {
		t.Error("an open instance is asking for an invitation")
	}

	resp, _ = client.post("/auth/oidc/complete", url.Values{"username": {"ada-lovelace"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("completing = %d, want 303", resp.StatusCode)
	}
	if _, err := h.store.UserByUsername(t.Context(), "ada-lovelace"); err != nil {
		t.Errorf("the account was not created: %v", err)
	}

	// The placeholder password cannot be guessed into an account.
	resp, _ = client.post("/login", url.Values{
		"username": {"ada-lovelace"},
		"password": {""},
	})
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("an account created through the provider signed in with an empty password")
	}
}

func TestOIDCRejectsAWrongStateOrNonce(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.registerForm("boss")

	// A callback whose state did not come from an attempt this server started.
	client := h.newSession(t)
	h.authorize(t, client.client, "")
	resp, _ := client.get("/auth/oidc/callback?code=a-code&state=not-the-state")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a forged state = %d, want a refusal", resp.StatusCode)
	}

	// A genuine state, but a token minted for a different attempt.
	authorizeURL := h.authorize(t, client.client, "")
	idp.setNonce("a-different-nonce")
	idp.setIdentity("subject-3", "three@example.com", true, "three")

	target := h.server.URL + "/auth/oidc/callback?code=a-code&state=" +
		url.QueryEscape(authorizeURL.Query().Get("state"))
	resp2, err := client.client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("a token for another attempt = %d, want a refusal", resp2.StatusCode)
	}
}

func TestOIDCRejectsADisallowedDomain(t *testing.T) {
	idp := newFakeIDP(t)
	h := newHarnessWith(t, func(cfg *config.Config) {
		cfg.OIDCIssuer = idp.server.URL
		cfg.OIDCClientID = idp.clientID
		cfg.OIDCClientSecret = idp.clientSecret
		cfg.OIDCAllowedDomains = []string{"allowed.example"}
	})
	h.registerForm("boss")

	idp.setIdentity("subject-out", "stranger@elsewhere.example", true, "stranger")

	client := h.newSession(t)
	resp := h.callback(t, idp, client.client, h.authorize(t, client.client, ""))

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a disallowed domain = %d, want a refusal", resp.StatusCode)
	}

	// The allowed domain still works.
	idp.setIdentity("subject-in", "person@allowed.example", true, "person")
	resp = h.callback(t, idp, client.client, h.authorize(t, client.client, ""))
	if !strings.HasSuffix(resp.Header.Get("Location"), "/auth/oidc/complete") {
		t.Errorf("an allowed domain went to %q", resp.Header.Get("Location"))
	}
}

func TestOIDCCanBeConnectedAndDisconnected(t *testing.T) {
	idp := newFakeIDP(t)
	h := oidcHarness(t, idp)
	h.registerForm("boss")

	idp.setIdentity("subject-connect", "boss@example.com", true, "boss")

	// Signed in already, so the identity attaches to this account rather than
	// to whoever the provider names.
	authorizeURL := h.authorize(t, h.client, "?connect=1")
	resp := h.callback(t, idp, h.client, authorizeURL)
	if resp.StatusCode != http.StatusSeeOther ||
		!strings.HasPrefix(resp.Header.Get("Location"), "/settings/account") {
		t.Fatalf("connect = %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := h.store.IdentityBySubject(t.Context(), idp.server.URL, "subject-connect")
	if err != nil {
		t.Fatalf("the identity was not connected: %v", err)
	}
	if identity.UserID != boss.ID {
		t.Errorf("connected to %d, want %d", identity.UserID, boss.ID)
	}

	// And the account page shows it, with a way to undo it.
	_, page := h.get("/settings/account")
	if !strings.Contains(page, "Connected sign-in") || !strings.Contains(page, "boss@example.com") {
		t.Error("the account page does not list the connection")
	}

	resp, _ = h.postForm("/settings/account/identities/"+itoa64(identity.ID)+"/delete", url.Values{
		"csrf_token": {h.csrf()},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("disconnect = %d", resp.StatusCode)
	}
	if _, err := h.store.IdentityBySubject(t.Context(), idp.server.URL, "subject-connect"); err == nil {
		t.Error("the connection survived being disconnected")
	}
}

func TestOIDCIsAbsentWhenNotConfigured(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	if resp, _ := h.get("/auth/oidc/start"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("start = %d with no provider configured, want 404", resp.StatusCode)
	}

	_, page := h.newSession(t).get("/login")
	if strings.Contains(page, "/auth/oidc/start") {
		t.Error("the sign-in page offers a provider that is not configured")
	}
	if !strings.Contains(page, "Password") {
		t.Error("the sign-in page lost its password form")
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
