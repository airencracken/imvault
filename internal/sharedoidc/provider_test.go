// SPDX-License-Identifier: AGPL-3.0-or-later
package sharedoidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"imvault/internal/suite"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOIDCSignatureAudienceNonceAndPKCE(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			r.ParseForm()
			if r.Form.Get("code_verifier") != "verifier" {
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			claims := map[string]any{"iss": issuer, "aud": "client", "sub": "subject-123", "nonce": "nonce", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix()}
			switch r.Form.Get("code") {
			case "wrong-nonce":
				claims["nonce"] = "other"
			case "wrong-audience":
				claims["aud"] = "other"
			case "wrong-issuer":
				claims["iss"] = "https://other.example.org"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
			}
			header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test"})
			payload, _ := json.Marshal(claims)
			input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
			digest := sha256.Sum256([]byte(input))
			signature, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
			if r.Form.Get("code") == "bad-signature" {
				signature[0] ^= 1
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "unused", "token_type": "Bearer", "id_token": input + "." + base64.RawURLEncoding.EncodeToString(signature)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL
	p := &provider{config: suite.AuthConfig{Issuer: issuer, ClientID: "client", BaseURL: "https://app.example.org"}, client: server.Client()}
	destination, err := p.Authorize(context.Background(), "state", "nonce", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(destination)
	q := parsed.Query()
	if q.Get("nonce") != "nonce" || q.Get("state") != "state" || q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != "https://app.example.org/auth/shared/callback" {
		t.Fatal("authorization contract", q)
	}
	for _, code := range []string{"good", "wrong-nonce", "wrong-audience", "wrong-issuer", "expired", "bad-signature"} {
		t.Run(code, func(t *testing.T) {
			identity, err := p.Exchange(context.Background(), code, "nonce", "verifier")
			if code == "good" {
				if err != nil || identity.Subject != "subject-123" || identity.Issuer != issuer {
					t.Fatal(identity, err)
				}
			} else if err == nil {
				t.Fatal("unverified identity accepted")
			}
		})
	}
	if _, err := p.Exchange(context.Background(), "good", "nonce", "different-verifier"); err == nil {
		t.Fatal("wrong PKCE verifier accepted")
	}
	if strings.Contains(destination, "client_secret") {
		t.Fatal("credential in browser URL")
	}
}
