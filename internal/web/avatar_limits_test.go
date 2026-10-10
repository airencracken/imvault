// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"net/url"
	"strings"
	"testing"

	"imvault/internal/models"
)

func TestAvatarRequestBoundsAndFileAudience(t *testing.T) {
	h := newHarness(t)
	me, id := accountUnderTest(t, h)
	if err := h.store.SaveAvatar(t.Context(), id, avatarFixture(t)); err != nil {
		t.Fatal(err)
	}
	file := seedFileWithTag(t, h, "photo", &id, models.VisibilityPublic)
	resp, body := me.get("/f/" + file)
	if resp.StatusCode != 200 || !strings.Contains(body, `src="/avatars/`) {
		t.Fatal("signed-in file contributor avatar", resp.StatusCode, body)
	}
	guest := h.newSession(t)
	resp, body = guest.get("/f/" + file)
	if resp.StatusCode != 200 || strings.Contains(body, `src="/avatars/`) {
		t.Fatal("public file disclosed avatar", resp.StatusCode)
	}
	resp, _ = avatarPost(t, me, [][]byte{make([]byte, (3<<20)+1)}, nil, true)
	if resp.StatusCode != 413 {
		t.Fatal("request body limit", resp.StatusCode)
	}
	resp, _ = avatarPost(t, me, [][]byte{make([]byte, (2<<20)+1)}, nil, true)
	location, err := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 303 || err != nil || location.Query().Get("error") == "" {
		t.Fatal("per-image limit", resp.StatusCode, err)
	}
	if err := h.store.SetAnimateAvatars(t.Context(), id, false); err != nil {
		t.Fatal(err)
	}
	resp, body = me.post("/settings/profile", url.Values{"profile_name": {strings.Repeat("x", 81)}})
	if resp.StatusCode != 422 || !strings.Contains(body, `src="/avatars/`) || strings.Contains(body, `name="animate" value="1" checked`) {
		t.Fatal("invalid bio edit lost avatar or viewer preference", resp.StatusCode, body)
	}
}
