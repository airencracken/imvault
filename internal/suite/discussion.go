// SPDX-License-Identifier: AGPL-3.0-or-later
package suite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type pendingDiscussion struct {
	binding [32]byte
	source  string
	expires time.Time
}

var discussionReturns = struct {
	sync.Mutex
	pending map[string]pendingDiscussion
}{pending: map[string]pendingDiscussion{}}

// PrepareDiscussion adds a short-lived return address to a reviewed handoff.
// The source session remains necessary to save the resulting thread.
func PrepareDiscussion(handoff, source, binding string) (string, error) {
	if binding == "" {
		return handoff, nil
	}
	u, e := url.Parse(handoff)
	if e != nil {
		return "", e
	}
	s, e := url.Parse(source)
	if e != nil || !validAppURL(source) {
		return "", errors.New("invalid discussion source")
	}
	discussionReturns.Lock()
	defer discussionReturns.Unlock()
	now := time.Now()
	count := 0
	hash := sha256.Sum256([]byte(binding))
	for k, p := range discussionReturns.pending {
		if now.After(p.expires) {
			delete(discussionReturns.pending, k)
		} else if p.binding == hash {
			count++
		}
	}
	if len(discussionReturns.pending) >= 1024 || count >= 16 {
		return "", errors.New("too many pending discussions")
	}
	var random [32]byte
	if _, e = rand.Read(random[:]); e != nil {
		return "", e
	}
	token := hex.EncodeToString(random[:])
	discussionReturns.pending[token] = pendingDiscussion{hash, s.Path, now.Add(20 * time.Minute)}
	s.Path = strings.TrimRight(s.Path, "/") + "/discussion-return"
	q := s.Query()
	q.Set("state", token)
	s.RawQuery = q.Encode()
	q = u.Query()
	q.Set("return_to", s.String())
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// CheckDiscussion requires the originating session, entity and configured forum.
// Saving still uses the application's authenticated, CSRF-protected POST route.
func CheckDiscussion(token, sourcePath, binding, thread, forum string) bool {
	discussionReturns.Lock()
	defer discussionReturns.Unlock()
	p, ok := discussionReturns.pending[token]
	if !ok || time.Now().After(p.expires) || p.source != sourcePath || binding == "" || p.binding != sha256.Sum256([]byte(binding)) {
		return false
	}
	return validDiscussionThread(thread, forum)
}

func validDiscussionThread(thread, forum string) bool {
	base, e := url.Parse(forum)
	if e != nil || !validAppURL(forum) {
		return false
	}
	u, e := url.Parse(thread)
	if e != nil || u.User != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	prefix := strings.TrimRight(base.Path, "/") + "/topics/"
	if !strings.HasPrefix(u.Path, prefix) {
		return false
	}
	id, e := strconv.ParseInt(strings.TrimPrefix(u.Path, prefix), 10, 64)
	return e == nil && id > 0
}

func FinishDiscussion(token string) {
	discussionReturns.Lock()
	delete(discussionReturns.pending, token)
	discussionReturns.Unlock()
}

// DiscussionReturn validates a browser return destination against explicit app
// configuration and the source origin. No server request is made to that URL.
func DiscussionReturn(raw, source, apps string) (string, error) {
	if raw == "" {
		return "", nil
	}
	fail := errors.New("discussion return is not configured for this source")
	if len(raw) > 4096 {
		return "", fail
	}
	u, e := parseDiscussionReturn(raw, source)
	if e != nil {
		return "", fail
	}
	configured, e := ParseApps(apps)
	if e != nil {
		return "", e
	}
	for _, a := range configured {
		if discussionAppMatches(a, u) {
			return u.String(), nil
		}
	}
	return "", fail
}

func DiscussionResult(destination, thread string) string {
	u, e := url.Parse(destination)
	if e != nil || destination == "" {
		return ""
	}
	q := u.Query()
	q.Set("thread", thread)
	u.RawQuery = q.Encode()
	return u.String()
}

// ConfiguredDiscussionReturn validates a submitted return field again after
// posting, so changing hidden fields cannot introduce an arbitrary redirect.
func ConfiguredDiscussionReturn(raw, apps string) (string, error) {
	if raw == "" {
		return "", nil
	}
	u, e := url.Parse(raw)
	if e != nil {
		return "", e
	}
	u.Path = strings.TrimSuffix(u.Path, "/discussion-return")
	u.RawQuery = ""
	return DiscussionReturn(raw, u.String(), apps)
}

func parseDiscussionReturn(raw, source string) (*url.URL, error) {
	fail := errors.New("invalid discussion return")
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Fragment != "" || !strings.HasSuffix(u.Path, "/discussion-return") {
		return nil, fail
	}
	sourceURL, e := url.Parse(source)
	if e != nil || u.Scheme != sourceURL.Scheme || u.Host != sourceURL.Host || u.Path != strings.TrimRight(sourceURL.Path, "/")+"/discussion-return" {
		return nil, fail
	}
	q := u.Query()
	state := q.Get("state")
	if len(q) != 1 || len(q["state"]) != 1 || len(state) != 64 {
		return nil, fail
	}
	if _, e = hex.DecodeString(state); e != nil {
		return nil, fail
	}
	return u, nil
}
func discussionAppMatches(a App, u *url.URL) bool {
	base, _ := url.Parse(a.URL)
	return (a.ID == "imvault" || a.ID == "songstead") && u.Scheme == base.Scheme && u.Host == base.Host && strings.HasPrefix(u.Path, strings.TrimRight(base.Path, "/")+"/")
}
