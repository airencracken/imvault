// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/store"
)

// accountUnderTest provisions an administrator and then a second, ordinary
// account, and returns a session for the ordinary one.
//
// A separate administrator keeps ordinary account deletion independent of the
// store's last-administrator guard.
func accountUnderTest(t *testing.T, h *harness) (*session, int64) {
	t.Helper()

	h.provisionAdmin("boss")

	user := h.seedUser("marcus")
	if err := h.store.SetPassword(t.Context(), user.ID,
		mustHashPassword(t, testPassword)); err != nil {
		t.Fatalf("set password: %v", err)
	}
	return h.sessionFor(t, user.ID), user.ID
}

func TestAccountDeletionNeedsEveryConfirmation(t *testing.T) {
	h := newHarness(t)
	me, userID := accountUnderTest(t, h)

	resp, page := me.get("/settings/account")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/settings/account = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(page, "This cannot be undone") {
		t.Error("the page does not warn that it is permanent")
	}
	if !strings.Contains(page, "Type <strong>marcus</strong>") {
		t.Error("the page does not ask for the username to be typed")
	}

	alive := func(what string) {
		t.Helper()
		if _, err := h.store.UserByID(t.Context(), userID); err != nil {
			t.Fatalf("%s deleted the account", what)
		}
	}

	me.post("/settings/account/delete", url.Values{
		"password": {"not-the-password"},
		"confirm":  {"marcus"},
	})
	alive("a wrong password")

	me.post("/settings/account/delete", url.Values{
		"password": {testPassword},
		"confirm":  {"marcusx"},
	})
	alive("a mistyped username")

	me.post("/settings/account/delete", url.Values{
		"password": {testPassword},
	})
	alive("an empty username")

	resp, _ = me.post("/settings/account/delete", url.Values{
		"password": {testPassword},
		"confirm":  {"marcus"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete = %d, want 303", resp.StatusCode)
	}
	if _, err := h.store.UserByID(t.Context(), userID); err == nil {
		t.Fatal("the account survived a fully confirmed deletion")
	}
	if me.signedIn() {
		t.Error("the session outlived the account it belonged to")
	}
}

func TestAccountDeletionTakesTheFilesWithIt(t *testing.T) {
	h := newHarness(t)
	me, userID := accountUnderTest(t, h)

	resp, body := me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "one.png", data: pngFixture(t, 40, 40)},
		{name: "two.png", data: pngFixture(t, 40, 40)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d (body: %s)", resp.StatusCode, truncate(body))
	}

	files, err := h.store.ListFiles(t.Context(), store.FileQuery{OwnerID: &userID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected two files, got %d", len(files))
	}

	if before := countStoredObjects(t, h.dataDir); before == 0 {
		t.Fatal("no objects were written")
	}

	_, page := me.get("/settings/account")
	if !strings.Contains(page, "2 uploaded files") {
		t.Errorf("the page does not say how much will be removed: %s", truncate(page))
	}

	me.post("/settings/account/delete", url.Values{
		"password": {testPassword},
		"confirm":  {"marcus"},
	})

	// The rows cascade, but the bytes live outside the database, so the objects
	// are the part worth checking.
	if after := countStoredObjects(t, h.dataDir); after != 0 {
		t.Errorf("%d objects were orphaned on disk", after)
	}
	for _, file := range files {
		if _, err := h.store.FileByID(t.Context(), file.ID); err == nil {
			t.Errorf("file %s survived the account it belonged to", file.ID)
		}
	}
}

func TestAccountDeletionNeedsTheSecondFactor(t *testing.T) {
	h := newHarness(t)
	me, userID := accountUnderTest(t, h)

	secret := h.enableTwoFactorFor(t, userID)
	_ = secret

	alive := func(what string) {
		t.Helper()
		if _, err := h.store.UserByID(t.Context(), userID); err != nil {
			t.Fatalf("%s deleted the account", what)
		}
	}

	// Password and username, but no code.
	me.post("/settings/account/delete", url.Values{
		"password": {testPassword},
		"confirm":  {"marcus"},
	})
	alive("a missing second factor")

	// A wrong code.
	me.post("/settings/account/delete", url.Values{
		"password": {testPassword},
		"confirm":  {"marcus"},
		"code":     {"000000"},
	})
	alive("a wrong second factor")

	// A recovery code counts.
	user, err := h.store.UserByID(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	codes := h.recoveryCodesFor(t, user.ID)
	if len(codes) == 0 {
		t.Fatal("no recovery codes were issued")
	}

	me.post("/settings/account/delete", url.Values{
		"password": {testPassword},
		"confirm":  {"marcus"},
		"code":     {codes[0]},
	})
	if _, err := h.store.UserByID(t.Context(), userID); err == nil {
		t.Error("a fully confirmed deletion did not delete the account")
	}
}

func TestLastAdministratorCannotDeleteThemselves(t *testing.T) {
	h := newHarness(t)
	boss := h.provisionAdmin("boss")

	resp, _ := h.postForm("/settings/account/delete", url.Values{
		"csrf_token": {h.csrf()},
		"password":   {testPassword},
		"confirm":    {"boss"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete = %d, want 303", resp.StatusCode)
	}

	// The instance would be left with nobody able to administer it.
	if _, err := h.store.UserByID(t.Context(), boss.ID); err != nil {
		t.Fatal("the last administrator deleted themselves")
	}
	if location := resp.Header.Get("Location"); !strings.Contains(location, "error=") {
		t.Errorf("redirected to %q, want an error explaining why", location)
	}
}

func TestDeletingAnAccountFreesItsTagsAndKeys(t *testing.T) {
	h := newHarness(t)
	me, userID := accountUnderTest(t, h)

	resp, body := me.upload(map[string]string{"public": "1"}, []uploadFile{
		{name: "one.png", data: pngFixture(t, 40, 40)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d", resp.StatusCode)
	}
	id := firstFileID(t, body)

	// A tag and an API key, both of which belong to the account.
	me.post("/f/"+id+"/tags", url.Values{"name": {"holiday"}})

	if _, err := h.store.CreateAPIKey(t.Context(), userID, "laptop", "abc123def456", "hash", nil); err != nil {
		t.Fatal(err)
	}

	// The administrator can see the tag while the file is public.
	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	before, err := h.store.ListTags(t.Context(), &boss.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 {
		t.Fatalf("expected the admin to see one public tag, got %d", len(before))
	}

	me.post("/settings/account/delete", url.Values{
		"password": {testPassword},
		"confirm":  {"marcus"},
	})

	tags, err := h.store.ListTags(t.Context(), &boss.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Errorf("%d tags outlived the account: %+v", len(tags), tags)
	}

	keys, err := h.store.APIKeysByUser(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("%d API keys outlived the account", len(keys))
	}
}
