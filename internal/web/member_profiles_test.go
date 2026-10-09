// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestMemberProfileRoutesOwnershipPrivacyAndExport(t *testing.T) {
	h := newHarness(t)
	me, id := accountUnderTest(t, h)
	otherUser := h.seedUser("another")
	other := h.sessionFor(t, otherUser.ID)
	path := fmt.Sprintf("/members/%d", id)
	resp, body := other.get(path)
	if resp.StatusCode != 200 || !strings.Contains(body, "hasn’t added a bio") {
		t.Fatal("optional blank profile", resp.StatusCode, body)
	}
	form := url.Values{"profile_name": {"<script>name</script>"}, "profile_bio": {"A short bio.\n<script>bio</script>"}, "profile_link_label": {"My site"}, "profile_link_url": {"https://example.org/?x=1&y=2"}, "user_id": {fmt.Sprint(otherUser.ID)}}
	resp, body = me.post("/settings/profile", form)
	if resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/settings/profile?") {
		t.Fatal("save contract", resp.StatusCode, body)
	}
	savedLocation := resp.Header.Get("Location")
	before, err := h.store.MemberProfile(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	untouched, err := h.store.MemberProfile(t.Context(), otherUser.ID)
	if err != nil || untouched.Biography.Name != "" {
		t.Fatal("edit targeted another member", untouched, err)
	}
	resp, body = other.get(path)
	for _, text := range []string{"&lt;script&gt;name&lt;/script&gt;", "&lt;script&gt;bio&lt;/script&gt;", `href="https://example.org/?x=1&amp;y=2"`, `target="_blank"`, `rel="noopener noreferrer ugc nofollow"`, `hx-boost="false"`, "signed-in members"} {
		if !strings.Contains(body, text) {
			t.Fatal("profile missing safe content", text, body)
		}
	}
	if strings.Contains(body, "<script>bio") || strings.Contains(body, "Edit your profile") || strings.Contains(body, "marcus@example") || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("profile privacy or escaping", body)
	}
	guest := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = guest.Get(h.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	body = readAll(t, resp.Body)
	mustClose(t, resp.Body)
	if resp.StatusCode != 303 || strings.Contains(body, "short bio") {
		t.Fatal("guest profile disclosure", resp.StatusCode, body)
	}
	for _, invalid := range []string{"0", "-1", "abc", "9223372036854775808", "99999"} {
		resp, _ := other.get("/members/" + invalid)
		if resp.StatusCode != 404 {
			t.Fatal("unavailable profile", invalid, resp.StatusCode)
		}
	}
	req, err := http.NewRequest("HEAD", h.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = other.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || readAll(t, resp.Body) != "" {
		t.Fatal("HEAD profile route", resp.StatusCode)
	}
	mustClose(t, resp.Body)
	resp, _ = other.post(path, url.Values{})
	if resp.StatusCode != 405 {
		t.Fatal("profile method contract", resp.StatusCode)
	}
	for _, bad := range []url.Values{{"profile_name": {"one", "two"}}, {"profile_bio": {"one", "two"}}, {"profile_link_label": {"Site"}, "profile_link_url": {"javascript:bad"}}, {"profile_bio": {strings.Repeat("x", 1001)}}} {
		resp, body = me.post("/settings/profile", bad)
		if resp.StatusCode != 422 {
			t.Fatal("invalid profile accepted", bad, resp.StatusCode, body)
		}
		after, err := h.store.MemberProfile(t.Context(), id)
		if err != nil || !reflect.DeepEqual(after, before) {
			t.Fatal("invalid edit changed stored profile", after, err)
		}
	}
	req, err = http.NewRequest("POST", h.server.URL+"/settings/profile", strings.NewReader("profile_name=Hacked"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = me.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Fatal("CSRF bypass", resp.StatusCode)
	}
	mustClose(t, resp.Body)
	resp, body = me.get(savedLocation)
	if resp.StatusCode != 200 || !strings.Contains(body, "Profile saved.") || strings.Count(body, `name="profile_link_url"`) != 5 {
		t.Fatal("profile settings", resp.StatusCode, body)
	}
	resp, body = me.get("/settings/account")
	if resp.StatusCode != 200 || !strings.Contains(body, `href="/settings/profile"`) || !strings.Contains(body, `href="`+path+`"`) {
		t.Fatal("profile settings discoverability", resp.StatusCode, body)
	}
	_, entries, _ := readExport(t, me)
	var archive exportManifest
	if err = json.Unmarshal(entries["manifest.json"], &archive); err != nil || !reflect.DeepEqual(archive.Account.Profile, before.Biography) {
		t.Fatal("own profile export", archive, err)
	}
	_, _, otherExport := readExport(t, other)
	if strings.Contains(string(otherExport), "short bio") {
		t.Fatal("another member's export contains profile")
	}
}
