// SPDX-License-Identifier: AGPL-3.0-or-later
package suite

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthFlowBindingExpiryReplayAndAdmission(t *testing.T) {
	a := &Auth{config: AuthConfig{App: "songstead", BaseURL: "https://music.example.org"}, flows: map[string]authFlow{}}
	state := strings.Repeat("a", 64)
	a.admit(state, authFlow{Expires: time.Now().Add(time.Minute)})
	request := httptest.NewRequest(http.MethodGet, "https://music.example.org/auth/shared/callback?state="+state, nil)
	request.AddCookie(&http.Cookie{Name: a.cookieName(), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Value: strings.Repeat("b", 64)})
	if _, ok := a.takeFlow(httptest.NewRecorder(), request); ok {
		t.Fatal("flow accepted a different browser cookie")
	}
	request = httptest.NewRequest(http.MethodGet, "https://music.example.org/auth/shared/callback?state="+state, nil)
	request.AddCookie(&http.Cookie{Name: a.cookieName(), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Value: state})
	w := httptest.NewRecorder()
	if _, ok := a.takeFlow(w, request); !ok {
		t.Fatal("valid browser flow refused")
	}
	if _, ok := a.takeFlow(w, request); ok {
		t.Fatal("callback replay accepted")
	}
	a.flows[state] = authFlow{Expires: time.Now().Add(-time.Minute)}
	if _, ok := a.takeFlow(w, request); ok {
		t.Fatal("expired callback accepted")
	}
	for i := range 16 {
		if !a.admit(strings.Repeat("x", i+1), authFlow{Remote: "same", Expires: time.Now().Add(time.Minute)}) {
			t.Fatal("early admission refusal")
		}
	}
	if a.admit("overflow", authFlow{Remote: "same", Expires: time.Now().Add(time.Minute)}) {
		t.Fatal("unbounded sign-in requests")
	}
	if remoteAuthKey("192.0.2.1:123") != remoteAuthKey("192.0.2.1:456") {
		t.Fatal("changing a source port bypassed admission limits")
	}
	a.flowCookie(w, state, 300)
	for _, cookie := range w.Result().Cookies() {
		if cookie.Value == state && (!cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/") {
			t.Fatal("unprotected sign-in cookie")
		}
	}
}

func TestAuthUsesApplicationNetworkPolicy(t *testing.T) {
	a := &Auth{}
	r := httptest.NewRequest(http.MethodGet, "https://app.example/auth/shared/start", nil)
	r.RemoteAddr = "192.0.2.1:123"
	r.Header.Set("X-Forwarded-For", "203.0.113.8")
	if a.requestKey(r) != "192.0.2.1" {
		t.Fatal("untrusted forwarding header changed the default key")
	}
	a.hooks.RateKey = func(*http.Request) string { return "trusted-client-network" }
	if a.requestKey(r) != "trusted-client-network" {
		t.Fatal("application network policy ignored")
	}
}
