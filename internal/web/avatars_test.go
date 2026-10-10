// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func avatarFixture(t *testing.T) []byte {
	t.Helper()
	v := &gif.GIF{LoopCount: 0}
	for i := 0; i < 3; i++ {
		p := image.NewPaletted(image.Rect(0, 0, 16, 16), color.Palette{color.Black, color.White})
		p.SetColorIndex(i, 0, 1)
		v.Image = append(v.Image, p)
		v.Delay = append(v.Delay, 1)
	}
	var out bytes.Buffer
	if err := gif.EncodeAll(&out, v); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func avatarPost(t *testing.T, s *session, files [][]byte, form url.Values, csrf bool) (*http.Response, string) {
	t.Helper()
	var body bytes.Buffer
	m := multipart.NewWriter(&body)
	if csrf {
		if err := m.WriteField("csrf_token", s.token()); err != nil {
			t.Fatal(err)
		}
	}
	for k, vs := range form {
		for _, v := range vs {
			if err := m.WriteField(k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, data := range files {
		p, err := m.CreateFormFile("avatar", "picture.gif")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", s.h.server.URL+"/settings/avatar", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", m.FormDataContentType())
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer mustClose(t, resp.Body)
	return resp, readAll(t, resp.Body)
}

func TestAvatarRoutesPreferencesPrivacyExportAndRemoval(t *testing.T) {
	h := newHarness(t)
	me, id := accountUnderTest(t, h)
	user := h.seedUser("another")
	other := h.sessionFor(t, user.ID)
	data := avatarFixture(t)
	resp, body := avatarPost(t, me, [][]byte{data}, url.Values{"user_id": {fmt.Sprint(user.ID)}}, true)
	if resp.StatusCode != 303 {
		t.Fatal("upload", resp.StatusCode, body)
	}
	location := resp.Header.Get("Location")
	resp, body = me.get(location)
	if resp.StatusCode != 200 || !strings.Contains(body, "Avatar saved.") || !strings.Contains(body, `media="(prefers-reduced-motion: reduce)"`) || !strings.Contains(body, `name="animate" value="1" checked`) {
		t.Fatal("settings avatar", resp.StatusCode, body)
	}
	path := fmt.Sprintf("/avatars/%d", id)
	resp, raw := other.getBytes(path)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/gif" {
		t.Fatal("animated avatar", resp.StatusCode, resp.Header)
	}
	gifImage, err := gif.DecodeAll(bytes.NewReader(raw))
	if err != nil || len(gifImage.Image) != 3 {
		t.Fatal("GIF lost", err)
	}
	for _, delay := range gifImage.Delay {
		if delay < 10 {
			t.Fatal("frame rate unbounded")
		}
	}
	animationETag := resp.Header.Get("ETag")
	resp, still := other.getBytes(path + "?still=1")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || bytes.Equal(still, raw) || resp.Header.Get("ETag") == animationETag {
		t.Fatal("still variant", resp.StatusCode)
	}
	resp, body = other.get(fmt.Sprintf("/members/%d", id))
	if resp.StatusCode != 200 || !strings.Contains(body, `src="`+path+`"`) {
		t.Fatal("profile avatar", resp.StatusCode, body)
	}
	if resp, _ := other.post("/settings/avatar-preference", url.Values{}); resp.StatusCode != 303 {
		t.Fatal("preference save", resp.StatusCode)
	}
	resp, picture := other.getBytes(path)
	if resp.Header.Get("Content-Type") != "image/png" || !bytes.Equal(picture, still) {
		t.Fatal("viewer preference ignored")
	}
	resp, _ = me.getBytes(path)
	if resp.Header.Get("Content-Type") != "image/gif" {
		t.Fatal("one viewer affected another")
	}
	req, err := http.NewRequest("HEAD", h.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = me.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Length") == "" || readAll(t, resp.Body) != "" {
		t.Fatal("HEAD avatar")
	}
	mustClose(t, resp.Body)
	req, err = http.NewRequest("GET", h.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", animationETag)
	resp, err = me.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 304 {
		t.Fatal("conditional avatar", resp.StatusCode)
	}
	mustClose(t, resp.Body)
	guest := h.newSession(t)
	resp, body = guest.get(path)
	if resp.StatusCode != 303 || bytes.Contains([]byte(body), raw) {
		t.Fatal("guest avatar disclosure", resp.StatusCode)
	}
	for _, invalid := range []string{"0", "-1", "x", "99999", "9223372036854775808"} {
		resp, _ := me.get("/avatars/" + invalid)
		if resp.StatusCode != 404 {
			t.Fatal("invalid avatar id", invalid, resp.StatusCode)
		}
	}
	_, entries, _ := readExport(t, me)
	var manifest exportManifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil || manifest.Account.AvatarPath != "avatar.png" || manifest.Account.AnimatedAvatarPath != "avatar.gif" || !manifest.Account.AnimateAvatars || !bytes.Equal(entries["avatar.png"], still) || !bytes.Equal(entries["avatar.gif"], raw) {
		t.Fatal("avatar export", manifest.Account, err)
	}
	_, entries, _ = readExport(t, other)
	if len(entries["avatar.png"]) != 0 || len(entries["avatar.gif"]) != 0 {
		t.Fatal("export contains another avatar")
	}
	before, err := h.store.Avatar(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, files := range [][][]byte{{data, data}, {[]byte("<svg onload=bad>")}, nil} {
		resp, _ := avatarPost(t, me, files, nil, true)
		location, parseErr := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != 303 || parseErr != nil || location.Query().Get("error") == "" || location.Query().Get("notice") != "" {
			t.Fatal("invalid upload needs rejection feedback", resp.StatusCode, resp.Header.Get("Location"), parseErr)
		}
		after, err := h.store.Avatar(t.Context(), id)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("invalid upload altered image", err)
		}
	}
	resp, _ = avatarPost(t, me, [][]byte{data}, url.Values{"remove": {"1"}}, true)
	after, err := h.store.Avatar(t.Context(), id)
	if resp.StatusCode != 303 || err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("ambiguous removal", resp.StatusCode, err)
	}
	resp, _ = avatarPost(t, me, [][]byte{data}, nil, false)
	if resp.StatusCode != 403 {
		t.Fatal("CSRF bypass", resp.StatusCode)
	}
	for _, form := range []url.Values{{"animate": {"0"}}, {"animate": {"1", "1"}}} {
		resp, _ := other.post("/settings/avatar-preference", form)
		if resp.StatusCode != 422 {
			t.Fatal("invalid preference", resp.StatusCode)
		}
	}
	if on, err := h.store.AnimateAvatars(t.Context(), user.ID); err != nil || on {
		t.Fatal("invalid preference changed viewer", err)
	}
	resp, _ = other.post(fmt.Sprintf("/admin/users/%d/avatar/remove", id), url.Values{})
	if resp.StatusCode != 403 {
		t.Fatal("member moderation", resp.StatusCode)
	}
	for _, route := range []string{"/settings/avatar", "/settings/avatar-preference"} {
		resp, _ := me.get(route)
		if resp.StatusCode != 405 {
			t.Fatal("method contract", route, resp.StatusCode)
		}
	}
	adminUser, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	admin := h.sessionFor(t, adminUser.ID)
	resp, _ = admin.post(fmt.Sprintf("/admin/users/%d/avatar/remove", id), url.Values{})
	if resp.StatusCode != 303 {
		t.Fatal("administrator removal", resp.StatusCode)
	}
	if err := h.store.SaveAvatar(t.Context(), id, data); err != nil {
		t.Fatal(err)
	}
	resp, _ = me.post("/settings/avatar", url.Values{"remove": {"1"}})
	if resp.StatusCode != 303 {
		t.Fatal("remove", resp.StatusCode)
	}
	resp, _ = me.get(path)
	if resp.StatusCode != 404 {
		t.Fatal("removed avatar served", resp.StatusCode)
	}
	if on, err := h.store.AnimateAvatars(t.Context(), user.ID); err != nil || on {
		t.Fatal("removal reset preference", err)
	}
}
