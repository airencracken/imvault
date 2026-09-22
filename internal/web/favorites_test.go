// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"imvault/internal/models"
	"imvault/internal/store"
)

func TestFavoritesArePersonalAndWorkWithoutJavaScript(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	bob := h.seedUser("bob")
	aliceSession, bobSession := h.sessionFor(t, alice.ID), h.sessionFor(t, bob.ID)
	id := uploadAs(t, bobSession, "holiday.png", "public")
	path := "/f/" + id

	_, page := aliceSession.get(path)
	if !strings.Contains(page, `aria-label="Add to favorites"`) {
		t.Fatal("signed-in viewer has no favorite control")
	}
	for i := 0; i < 2; i++ {
		resp, _ := aliceSession.post(path+"/favorite", url.Values{
			"favorite": {"1"}, "user_id": {itoa64(bob.ID)},
		})
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != path {
			t.Fatalf("favorite without JavaScript = %d, location=%q", resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	count, err := h.store.CountFiles(t.Context(), store.FileQuery{FavoritedBy: &alice.ID})
	if err != nil || count != 1 {
		t.Fatalf("duplicate favorite: count=%d err=%v", count, err)
	}
	_, page = aliceSession.get(path)
	if !strings.Contains(page, `aria-label="Remove from favorites"`) {
		t.Fatal("favorite state did not survive a page reload")
	}
	resp, page := aliceSession.get("/favorites?q=holiday")
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, `id="file-`+id+`"`) {
		t.Fatal("favorite missing from searchable list")
	}
	for _, target := range []string{"/favorites?user_id=" + itoa64(alice.ID), path} {
		_, page := bobSession.get(target)
		if strings.Contains(page, `id="file-`+id+`"`) || strings.Contains(page, `aria-label="Remove from favorites"`) {
			t.Error("another account can see Alice's favorite state")
		}
	}
	for i := 0; i < 2; i++ {
		if resp, _ := aliceSession.post(path+"/favorite", url.Values{"favorite": {"0"}}); resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("remove favorite = %d", resp.StatusCode)
		}
	}
	_, page = aliceSession.get("/favorites")
	if strings.Contains(page, `id="file-`+id+`"`) || !strings.Contains(page, "No favorites here yet") {
		t.Fatal("removed favorite is still listed")
	}
	if _, err := h.store.FileByID(t.Context(), id); err != nil {
		t.Fatal("removing a favorite removed the file")
	}
}

func TestFavoritesNeverGrantVisibilityOrExposeOtherLists(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("alice")
	bob := h.seedUser("bob")
	viewer, owner := h.sessionFor(t, alice.ID), h.sessionFor(t, bob.ID)
	id := uploadAs(t, owner, "shared.png", "members")
	path := "/f/" + id + "/favorite"
	if resp, _ := viewer.post(path, url.Values{"favorite": {"1"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("favorite members-only photo = %d", resp.StatusCode)
	}
	if err := h.store.SetFileVisibility(t.Context(), id, models.VisibilityPrivate); err != nil {
		t.Fatal(err)
	}
	_, page := viewer.get("/favorites")
	if strings.Contains(page, id) || strings.Contains(page, "shared.png") {
		t.Fatal("favorite list exposed a photo that became private")
	}
	for _, value := range []string{"0", "1"} {
		if resp, _ := viewer.post(path, url.Values{"favorite": {value}}); resp.StatusCode != http.StatusNotFound {
			t.Errorf("favorite private photo = %d, want 404", resp.StatusCode)
		}
	}
	if resp, _ := viewer.get("/f/" + id); resp.StatusCode != http.StatusNotFound {
		t.Fatal("favorite granted access to a private photo")
	}
	h.provisionAdmin("boss")
	_, page = h.get("/favorites?user_id=" + itoa64(alice.ID))
	if strings.Contains(page, id) {
		t.Fatal("admin favorite list exposed somebody else's favorites")
	}
	if resp, _ := h.postForm(path, url.Values{"favorite": {"1"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal("administrator could not favorite a photo they can view")
	}
	_, page = h.get("/favorites")
	if !strings.Contains(page, `id="file-`+id+`"`) {
		t.Fatal("administrator's own favorite missing")
	}
}

func TestFavoritesRequireSessionCSRFAndValidInput(t *testing.T) {
	h := newHarness(t)
	h.registerForm("alice")
	u, err := h.store.UserByUsername(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	owner := h.sessionFor(t, u.ID)
	id := uploadAs(t, owner, "public.png", "public")
	path := "/f/" + id + "/favorite"
	guest := h.newSession(t)
	if resp, _ := guest.get("/favorites"); resp.StatusCode != http.StatusSeeOther {
		t.Fatal("guest could open favorites")
	}
	if resp, _ := guest.post(path, url.Values{"favorite": {"1"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal("guest favorite did not require login")
	}
	_, page := guest.get("/f/" + id)
	if strings.Contains(page, `id="favorite-control"`) {
		t.Fatal("guest has an authenticated favorite control")
	}
	resp, err := h.client.PostForm(h.server.URL+path, url.Values{"favorite": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("favorite without CSRF = %d", resp.StatusCode)
	}
	for _, value := range []string{"", "true", "-1", "2"} {
		if resp, _ := owner.post(path, url.Values{"favorite": {value}}); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("favorite=%q = %d, want 400", value, resp.StatusCode)
		}
	}
	key := h.seedKey(u.ID, "test", nil)
	resp, _ = h.apiDo(http.MethodGet, "/favorites", key, nil, "")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatal("an API key authenticated a web-only favorites page")
	}
	if favorite, err := h.store.IsFavorite(t.Context(), u.ID, id); err != nil || favorite {
		t.Fatal("rejected requests changed favorite state")
	}
	if resp, _ := owner.post("/f/missing/favorite", url.Values{"favorite": {"1"}}); resp.StatusCode != http.StatusNotFound {
		t.Fatal("missing photo accepted a favorite")
	}
	file, err := h.store.FileByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	file.ID, file.ExpiresAt = "expired-favorite", &past
	if err := h.store.CreateFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if resp, _ := owner.post("/f/expired-favorite/favorite", url.Values{"favorite": {"1"}}); resp.StatusCode != http.StatusGone {
		t.Fatal("expired photo accepted a favorite")
	}
}

func TestFavoriteHTMXReturnsUpdatedControl(t *testing.T) {
	h := newHarness(t)
	user := h.registerForm("alice")
	id := uploadAs(t, h.sessionFor(t, user.ID), "picture.png", "private")
	for _, value := range []string{"1", "0"} {
		resp, fragment := h.postHTMX("/f/"+id+"/favorite", url.Values{
			"favorite": {value}, "csrf_token": {h.csrf()},
		})
		if resp.StatusCode != http.StatusOK || strings.Contains(fragment, "<html") || !strings.Contains(fragment, `id="favorite-control"`) {
			t.Fatalf("unexpected favorite fragment: %d %s", resp.StatusCode, truncate(fragment))
		}
		if strings.Contains(fragment, `aria-pressed="true"`) != (value == "1") {
			t.Fatal("favorite fragment has stale state")
		}
	}
}
