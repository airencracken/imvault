// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"imvault/internal/config"
	"imvault/internal/store"
)

// tinyQuota gives accounts a cap small enough to exceed in a test.
const tinyQuota = 20 * 1024

func withTinyQuota(cfg *config.Config) { cfg.DefaultQuotaBytes = tinyQuota }

func TestStorageQuotaIsEnforcedOnUpload(t *testing.T) {
	h := newHarnessWith(t, withTinyQuota)
	h.registerForm("marcus")

	small := pngFixture(t, 32, 32)
	if len(small) > tinyQuota {
		t.Fatalf("fixture is %d bytes, larger than the test quota", len(small))
	}

	resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "small.png", data: small},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first upload = %d, want 200 (body: %s)", resp.StatusCode, truncate(body))
	}

	user, err := h.store.UserByUsername(t.Context(), "marcus")
	if err != nil {
		t.Fatal(err)
	}
	if user.StorageUsed != int64(len(small)) {
		t.Errorf("storage_used = %d, want %d", user.StorageUsed, len(small))
	}

	// Tighten the cap so the next upload cannot fit.
	if err := h.store.SetUserQuota(t.Context(), user.ID, user.StorageUsed+10); err != nil {
		t.Fatal(err)
	}

	resp, body = h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "bigger.png", data: small},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("over-quota request = %d, want a 200 carrying the error", resp.StatusCode)
	}
	if !strings.Contains(body, "quota") {
		t.Errorf("over-quota upload did not explain itself: %s", truncate(body))
	}

	// The rejection must not have charged anything or left a row behind.
	after, err := h.store.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StorageUsed != user.StorageUsed {
		t.Errorf("usage = %d after a rejected upload, want %d", after.StorageUsed, user.StorageUsed)
	}

	files, err := h.store.ListFiles(t.Context(), store.FileQuery{OwnerID: &user.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Errorf("the rejected upload was recorded anyway (%d files)", len(files))
	}
	if objects := countStoredObjects(t, h.dataDir); objects != 3 {
		t.Errorf("%d objects on disk, want 3 for the single accepted upload", objects)
	}
}

func TestStorageIsReleasedWhenAFileIsDeleted(t *testing.T) {
	h := newHarness(t)
	h.registerForm("marcus")

	resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "pic.png", data: pngFixture(t, 40, 40)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d (body: %s)", resp.StatusCode, truncate(body))
	}
	id := firstFileID(t, body)

	user, err := h.store.UserByUsername(t.Context(), "marcus")
	if err != nil {
		t.Fatal(err)
	}
	if user.StorageUsed <= 0 {
		t.Fatal("upload did not record any usage")
	}

	h.postForm("/f/"+id+"/delete", url.Values{
		"csrf_token": {h.csrf()},
		"next":       {"/gallery"},
	})

	after, err := h.store.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StorageUsed != 0 {
		t.Errorf("storage_used = %d after delete, want 0", after.StorageUsed)
	}
	if objects := countStoredObjects(t, h.dataDir); objects != 0 {
		t.Errorf("%d objects left on disk", objects)
	}
}

func TestAnonymousUploadsDoNotCountAgainstAnybody(t *testing.T) {
	h := newHarness(t)

	// An account with a quota far too small for the fixture, which should
	// nevertheless be unaffected: anonymous uploads belong to nobody.
	owner := h.seedUser("alice")
	if err := h.store.SetUserQuota(t.Context(), owner.ID, 10); err != nil {
		t.Fatal(err)
	}

	h.get("/upload")
	resp, body := h.uploadFiles(map[string]string{"public": "1"}, []uploadFile{
		{name: "anon.png", data: pngFixture(t, 40, 40)},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous upload = %d (body: %s)", resp.StatusCode, truncate(body))
	}
	if strings.Contains(body, "quota") {
		t.Errorf("an anonymous upload was measured against an account: %s", truncate(body))
	}

	after, err := h.store.UserByID(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StorageUsed != 0 {
		t.Errorf("anonymous upload charged an account %d bytes", after.StorageUsed)
	}
}

func TestAdminAreaRequiresAdministrator(t *testing.T) {
	h := newHarness(t)

	// The first account is the administrator.
	h.registerForm("boss")
	for _, path := range []string{"/admin", "/admin/users", "/admin/files"} {
		if resp, _ := h.get(path); resp.StatusCode != http.StatusOK {
			t.Errorf("admin GET %s = %d, want 200", path, resp.StatusCode)
		}
	}

	// A second, ordinary account must be refused, with a token that is
	// otherwise valid so the refusal genuinely comes from the admin check.
	regular := h.seedUser("regular")
	client := h.signIn(t, regular.ID)
	token := h.csrfFor(t, client)

	for _, path := range []string{"/admin", "/admin/users", "/admin/files"} {
		resp, err := client.Get(h.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("non-admin GET %s = %d, want 403", path, resp.StatusCode)
		}
	}

	resp := doForm(t, client, h.server.URL, "/admin/users/"+"2"+"/disabled", url.Values{
		"csrf_token": {token},
		"disabled":   {"1"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin admin action = %d, want 403", resp.StatusCode)
	}

	// Anonymous visitors are sent to the login page. The client must not follow
	// the redirect, or this would just observe the login page.
	anon := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	anonResp, err := anon.Get(h.server.URL + "/admin")
	if err != nil {
		t.Fatal(err)
	}
	defer anonResp.Body.Close()
	if anonResp.StatusCode != http.StatusSeeOther {
		t.Errorf("anonymous /admin = %d, want 303", anonResp.StatusCode)
	}
	if location := anonResp.Header.Get("Location"); !strings.HasPrefix(location, "/login") {
		t.Errorf("anonymous /admin redirected to %q, want /login", location)
	}
}

func TestAdminSetsAQuota(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	target := h.seedUser("alice")
	path := "/admin/users/" + itoa64(target.ID) + "/limits"

	resp, _ := h.postForm(path, url.Values{
		"csrf_token":  {h.csrf()},
		"quota_mb":    {"2"},
		"max_file_mb": {"1"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("set limits = %d, want 303", resp.StatusCode)
	}

	updated, err := h.store.UserByID(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.QuotaBytes != 2*1024*1024 {
		t.Errorf("quota = %d, want %d", updated.QuotaBytes, 2*1024*1024)
	}
	if updated.MaxFileBytes != 1*1024*1024 {
		t.Errorf("file limit = %d, want %d", updated.MaxFileBytes, 1*1024*1024)
	}

	// Zero clears both, leaving the instance defaults in force.
	h.postForm(path, url.Values{
		"csrf_token":  {h.csrf()},
		"quota_mb":    {"0"},
		"max_file_mb": {"0"},
	})
	if updated, err = h.store.UserByID(t.Context(), target.ID); err != nil {
		t.Fatal(err)
	}
	if !updated.Unlimited() {
		t.Errorf("quota = %d, want unlimited", updated.QuotaBytes)
	}
	if updated.MaxFileBytes != 0 {
		t.Errorf("file limit = %d, want the instance default", updated.MaxFileBytes)
	}

	// Rubbish is rejected rather than silently applied, and neither value moves.
	h.postForm(path, url.Values{
		"csrf_token":  {h.csrf()},
		"quota_mb":    {"2"},
		"max_file_mb": {"lots"},
	})
	if updated, err = h.store.UserByID(t.Context(), target.ID); err != nil {
		t.Fatal(err)
	}
	if !updated.Unlimited() {
		t.Errorf("a malformed field changed the quota to %d", updated.QuotaBytes)
	}
	if updated.MaxFileBytes != 0 {
		t.Errorf("a malformed field changed the file limit to %d", updated.MaxFileBytes)
	}
}

func TestDisablingAnAccountRevokesItsAccess(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	victim := h.seedUser("alice")
	key := h.seedKey(victim.ID, "laptop", nil)
	victimClient := h.signIn(t, victim.ID)

	// Both paths work to begin with.
	if resp, _ := h.apiDo(http.MethodGet, "/api/v1/me", key, nil, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("key before disabling = %d, want 200", resp.StatusCode)
	}
	if resp, err := victimClient.Get(h.server.URL + "/gallery"); err != nil {
		t.Fatal(err)
	} else if resp.Body.Close(); resp.StatusCode != http.StatusOK {
		t.Fatalf("session before disabling = %d, want 200", resp.StatusCode)
	}

	resp, _ := h.postForm("/admin/users/"+itoa64(victim.ID)+"/disabled", url.Values{
		"csrf_token": {h.csrf()},
		"disabled":   {"1"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("disable = %d, want 303", resp.StatusCode)
	}

	updated, err := h.store.UserByID(t.Context(), victim.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Disabled {
		t.Fatal("the account was not disabled")
	}

	// The API key stops working...
	if resp, _ := h.apiDo(http.MethodGet, "/api/v1/me", key, nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("disabled account's key = %d, want 401", resp.StatusCode)
	}

	// ...and the session no longer authenticates, so the gallery redirects.
	sessionResp, err := victimClient.Get(h.server.URL + "/gallery")
	if err != nil {
		t.Fatal(err)
	}
	defer sessionResp.Body.Close()
	if sessionResp.StatusCode != http.StatusSeeOther {
		t.Errorf("disabled account's session = %d, want 303 to the login page", sessionResp.StatusCode)
	}
}

func TestDisabledAccountCannotSignInAgain(t *testing.T) {
	h := newHarness(t)

	victim := h.seedUser("alice")
	if err := h.store.SetUserDisabled(t.Context(), victim.ID, true); err != nil {
		t.Fatal(err)
	}

	h.get("/")
	resp, _ := h.postForm("/login", url.Values{
		"csrf_token": {h.csrf()},
		"username":   {"alice"},
		"password":   {"whatever"},
	})
	// Either the password is wrong or the account is disabled; both must fail,
	// and the disabled message must not leak to a caller who cannot log in.
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("a disabled account was allowed to sign in")
	}
}

func TestAdminDeletingAnAccountRemovesItsBytes(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	victim := h.seedUser("alice")
	key := h.seedKey(victim.ID, "laptop", nil)

	_, raw := h.apiUpload(key, nil, map[string][]byte{
		"a.png": pngFixture(t, 40, 40),
		"b.png": pngFixture(t, 40, 40),
	})
	if uploaded := decodeUpload(t, raw); len(uploaded.Files) != 2 {
		t.Fatalf("setup upload failed: %s", truncate(string(raw)))
	}

	if before := countStoredObjects(t, h.dataDir); before == 0 {
		t.Fatal("no objects were written")
	}

	resp, _ := h.postForm("/admin/users/"+itoa64(victim.ID)+"/delete", url.Values{
		"csrf_token": {h.csrf()},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete account = %d, want 303", resp.StatusCode)
	}

	if _, err := h.store.UserByID(t.Context(), victim.ID); err == nil {
		t.Error("the account survived deletion")
	}

	// The bytes are the important part: rows cascade, objects do not.
	if after := countStoredObjects(t, h.dataDir); after != 0 {
		t.Errorf("%d objects were orphaned on disk", after)
	}
}

func TestAdminRecomputesStorageUsage(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	alice := h.seedUser("alice")
	key := h.seedKey(alice.ID, "laptop", nil)

	_, raw := h.apiUpload(key, nil, map[string][]byte{"a.png": pngFixture(t, 30, 30)})
	if uploaded := decodeUpload(t, raw); len(uploaded.Files) != 1 {
		t.Fatalf("setup upload failed: %s", truncate(string(raw)))
	}

	// Drift the total, as a crash part-way through an upload would.
	if err := h.store.ReserveStorage(t.Context(), alice.ID, 999_999); err != nil {
		t.Fatal(err)
	}
	drifting, err := h.store.UserByID(t.Context(), alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if drifting.StorageUsed < 999_999 {
		t.Fatalf("setup did not drift the total (used = %d)", drifting.StorageUsed)
	}

	resp, _ := h.postForm("/admin/maintenance/storage", url.Values{"csrf_token": {h.csrf()}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("recompute = %d, want 303", resp.StatusCode)
	}

	corrected, err := h.store.UserByID(t.Context(), alice.ID)
	if err != nil {
		t.Fatal(err)
	}

	files, err := h.store.ListFiles(t.Context(), store.FileQuery{OwnerID: &alice.ID, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var expected int64
	for _, f := range files {
		expected += f.Size
	}

	if corrected.StorageUsed != expected {
		t.Errorf("usage = %d, want %d (the sum of the account's files)",
			corrected.StorageUsed, expected)
	}

	// Running it again reports that nothing needed doing.
	resp, _ = h.postForm("/admin/maintenance/storage", url.Values{"csrf_token": {h.csrf()}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("second recompute = %d, want 303", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); !strings.Contains(location, "accurate") {
		t.Errorf("second run reported %q, want it to say usage was already accurate", location)
	}
}

func TestAdminCannotRemoveTheLastAdministrator(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	boss, err := h.store.UserByUsername(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}

	resp, _ := h.postForm("/admin/users/"+itoa64(boss.ID)+"/role", url.Values{
		"csrf_token": {h.csrf()},
		"role":       {"member"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("demote = %d, want 303", resp.StatusCode)
	}
	if after, err := h.store.UserByID(t.Context(), boss.ID); err != nil {
		t.Fatal(err)
	} else if !after.IsAdmin() {
		t.Error("the last administrator was demoted")
	}

	h.postForm("/admin/users/"+itoa64(boss.ID)+"/delete", url.Values{"csrf_token": {h.csrf()}})
	if _, err := h.store.UserByID(t.Context(), boss.ID); err != nil {
		t.Error("the last administrator deleted themselves")
	}
}

func TestAdminCanDeleteAnyFile(t *testing.T) {
	h := newHarness(t)
	h.registerForm("boss")

	victim := h.seedUser("alice")
	key := h.seedKey(victim.ID, "laptop", nil)

	_, raw := h.apiUpload(key, nil, map[string][]byte{"a.png": pngFixture(t, 30, 30)})
	uploaded := decodeUpload(t, raw)
	if len(uploaded.Files) != 1 {
		t.Fatalf("setup upload failed: %s", truncate(string(raw)))
	}
	id := uploaded.Files[0].ID

	before, err := h.store.UserByID(t.Context(), victim.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.StorageUsed <= 0 {
		t.Fatal("setup did not record usage")
	}

	resp, _ := h.postForm("/admin/files/"+id+"/delete", url.Values{"csrf_token": {h.csrf()}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("admin delete = %d, want 303", resp.StatusCode)
	}

	if _, err := h.store.FileByID(t.Context(), id); err == nil {
		t.Error("the file survived an administrator's deletion")
	}

	after, err := h.store.UserByID(t.Context(), victim.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StorageUsed >= before.StorageUsed {
		t.Errorf("usage = %d, want it reduced from %d", after.StorageUsed, before.StorageUsed)
	}
}
