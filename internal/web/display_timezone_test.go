package web

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDisplayTimezoneAccountRoutesAndRendering(t *testing.T) {
	h := newHarness(t)
	u := h.provisionAdmin("timezone_owner")
	if response, body := h.get("/settings/account"); response.StatusCode != 200 || !strings.Contains(body, `value="UTC"`) {
		t.Fatal("missing default preference", response.StatusCode)
	}
	if response, body := h.postForm("/settings/account/timezone", url.Values{"timezone": {"America/Los_Angeles"}, "user_id": {"9999"}}); response.StatusCode != 303 {
		t.Fatal(response.StatusCode, body)
	}
	if zone, err := h.store.Timezone(t.Context(), u.ID); err != nil || zone != "America/Los_Angeles" {
		t.Fatal(zone, err)
	}
	if zone, _ := h.store.Timezone(t.Context(), 9999); zone != "UTC" {
		t.Fatal("changed another account")
	}
	id := uploadPlain(t, h, map[string]string{"visibility": "public"})
	stamp := time.Date(2026, 1, 1, 0, 15, 0, 0, time.UTC)
	if _, err := h.store.DB().Exec("UPDATE files SET created_at=? WHERE id=?", stamp.Unix(), id); err != nil {
		t.Fatal(err)
	}
	if response, body := h.get("/f/" + id); response.StatusCode != 200 || !strings.Contains(body, "Dec 31, 2025 · 16:15 PST") {
		t.Fatal("local date disagreement", response.StatusCode, body)
	}
	for _, values := range []url.Values{{"timezone": {"Local"}}, {"timezone": {"UTC", "Asia/Tokyo"}}, {"timezone": {"<script>bad</script>"}}} {
		response, body := h.postForm("/settings/account/timezone", values)
		if response.StatusCode != 422 || strings.Contains(body, "<script>bad</script>") {
			t.Fatal("invalid preference accepted or unescaped", response.StatusCode, body)
		}
		if zone, _ := h.store.Timezone(t.Context(), u.ID); zone != "America/Los_Angeles" {
			t.Fatal("invalid save changed preference")
		}
	}
	if _, err := h.store.DB().Exec("UPDATE users SET disabled=1 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	if h.store.SetTimezone(t.Context(), u.ID, "UTC") == nil {
		t.Fatal("disabled account changed preference")
	}
}
