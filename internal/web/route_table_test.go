// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

// routePattern matches a registration in routes().
var routePattern = regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+) ([^"]+)"`)

// registeredRoutes reads the routing table from its source, so a route added
// there is covered here without anybody remembering to list it.
func registeredRoutes(t *testing.T) [][2]string {
	t.Helper()
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var routes [][2]string
	for _, m := range routePattern.FindAllStringSubmatch(string(source), -1) {
		routes = append(routes, [2]string{m[1], m[2]})
	}
	if len(routes) < 50 {
		t.Fatalf("found only %d routes; the pattern no longer matches server.go", len(routes))
	}
	return routes
}

// concretePath fills a pattern's wildcards with placeholder values.
func concretePath(pattern string) string {
	pattern = strings.TrimSuffix(pattern, "{$}")
	return regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(pattern, "x")
}

// Every registered route answers its method through the router: whatever the
// handler then says, it is not the router's own 404 or 405. The routes this
// round of changes added are named so a refactor cannot quietly drop them.
func TestEveryRegisteredRouteIsReachable(t *testing.T) {
	h := newHarness(t)
	admin := h.provisionAdmin("boss")
	s := h.sessionFor(t, admin.ID)

	routes := registeredRoutes(t)
	for _, required := range [][2]string{
		{"POST", "/settings/reauth"},
		{"GET", "/invites"}, {"POST", "/invites"}, {"POST", "/invites/{id}/revoke"},
		{"GET", "/admin/invites"}, {"POST", "/admin/invites"}, {"POST", "/admin/invites/{id}/revoke"},
	} {
		found := false
		for _, route := range routes {
			found = found || route == required
		}
		if !found {
			t.Errorf("%s %s is not registered", required[0], required[1])
		}
	}

	for _, route := range routes {
		method, path := route[0], concretePath(route[1])
		if strings.HasPrefix(path, "/static/") {
			path = "/static/js/app.js"
		}
		req, err := http.NewRequest(method, h.server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(csrfHeader, s.token())
		resp, err := s.client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		body := readAll(t, resp.Body)
		mustClose(t, resp.Body)
		if resp.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf("%s %s was refused by the router: %d", method, route[1], resp.StatusCode)
		}
		if resp.StatusCode == http.StatusNotFound && body == "404 page not found\n" &&
			!s.routerKnows(path) {
			// A handler for a feature that is switched off answers with the
			// same plain 404 as the router, so ask the router directly.
			t.Errorf("%s %s was not routed", method, route[1])
		}
	}
}

// routerKnows reports whether the router has any route for a path, by asking
// with a method nothing uses: a known path answers 405, an unknown one 404.
func (s *session) routerKnows(path string) bool {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodPut, s.h.server.URL+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set(csrfHeader, s.token())
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	mustClose(s.t, resp.Body)
	return resp.StatusCode == http.StatusMethodNotAllowed
}
