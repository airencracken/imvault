// SPDX-License-Identifier: AGPL-3.0-or-later
package suite

import (
	"net/url"
	"strings"
	"testing"
)

func TestDiscussionRoundTrip(t *testing.T) {
	source := "https://music.example/recommendations/8"
	forum := "https://board.example"
	thread := forum + "/topics/42"
	link, e := PrepareDiscussion(forum+"/share?source="+url.QueryEscape(source), source, "local-session")
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(link)
	raw := u.Query().Get("return_to")
	destination, e := DiscussionReturn(raw, source, `[{"id":"songstead","url":"https://music.example"}]`)
	if e != nil {
		t.Fatal(e)
	}
	result, _ := url.Parse(DiscussionResult(destination, thread))
	token := result.Query().Get("state")
	if !CheckDiscussion(token, "/recommendations/8", "local-session", thread, forum) {
		t.Fatal("valid return rejected")
	}
	for _, v := range []struct{ path, binding, thread string }{{"/recommendations/9", "local-session", thread}, {"/recommendations/8", "other-session", thread}, {"/recommendations/8", "local-session", "https://evil.example/topics/42"}, {"/recommendations/8", "local-session", forum + "/topics/42?next=evil"}, {"/recommendations/8", "local-session", forum + "/topics/-1"}} {
		if CheckDiscussion(token, v.path, v.binding, v.thread, forum) {
			t.Fatal("forged return accepted", v)
		}
	}
	for _, v := range []string{"", `[{"id":"imvault","url":"https://other.example"}]`} {
		if _, e := DiscussionReturn(raw, source, v); e == nil {
			t.Fatal("unconfigured destination accepted")
		}
	}
	if _, e := DiscussionReturn(strings.Replace(raw, "/recommendations/8/", "/recommendations/9/", 1), source, `[{"id":"songstead","url":"https://music.example"}]`); e == nil {
		t.Fatal("changed entity accepted")
	}
	FinishDiscussion(token)
	if CheckDiscussion(token, "/recommendations/8", "local-session", thread, forum) {
		t.Fatal("return replay accepted")
	}
}
func FuzzDiscussionReturn(f *testing.F) {
	f.Add("https://bad.example/discussion-return", "https://music.example/recommendations/1")
	f.Fuzz(func(t *testing.T, raw, source string) {
		_, _ = DiscussionReturn(raw, source, `[{"id":"songstead","url":"https://music.example"}]`)
	})
}
