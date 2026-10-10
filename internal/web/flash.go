// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
)

// A flash is the one-line message a page shows after a redirect: "Album
// saved.", "That is not your current password." It travels in the query string
// so it survives the redirect without server-side state.
//
// It is signed. Without a signature the message was whatever a link said it
// was, so anybody could send a member a real page of this site announcing, in
// its own styling, that their account was suspended and they should email a
// stranger. Many messages carry live details (a count, a username), so a fixed
// table of keys could not express them; a signature lets the text vary while
// only this server can choose it.

// flashKind is whether a message reports success or a problem.
type flashKind string

const (
	flashNotice flashKind = "notice"
	flashError  flashKind = "error"

	// flashSigParam carries the signature alongside the message.
	flashSigParam = "sig"
)

// newFlashKey returns the key messages are signed with. It lives for the life
// of the process: a message in flight across a restart is simply not shown,
// which costs nothing worth a stored secret.
func newFlashKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("web: no randomness for the flash key: " + err.Error())
	}
	return key
}

// flashSignature authenticates one message of one kind.
func (s *Server) flashSignature(kind flashKind, message string) string {
	mac := hmac.New(sha256.New, s.flashKey)
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

// flashQuery adds a signed message to a set of query values.
func (s *Server) flashQuery(q url.Values, kind flashKind, message string) {
	if message == "" {
		return
	}
	q.Set(string(kind), message)
	q.Set(flashSigParam, s.flashSignature(kind, message))
}

// flashURL is path with a signed message attached.
func (s *Server) flashURL(path string, kind flashKind, message string) string {
	target, err := url.Parse(path)
	if err != nil {
		return path
	}
	q := target.Query()
	s.flashQuery(q, kind, message)
	target.RawQuery = q.Encode()
	return target.String()
}

// redirectFlash sends the browser to path with a message to show there.
func (s *Server) redirectFlash(w http.ResponseWriter, r *http.Request, path string, kind flashKind, message string) {
	http.Redirect(w, r, s.flashURL(path, kind, message), http.StatusSeeOther)
}

// flash returns the message a request carries, if this server signed it. An
// unsigned or altered message is not shown at all.
func (s *Server) flash(r *http.Request) (notice, problem string) {
	q := r.URL.Query()
	sig := q.Get(flashSigParam)
	if sig == "" {
		return "", ""
	}
	if message := strings.TrimSpace(q.Get(string(flashError))); message != "" &&
		hmac.Equal([]byte(sig), []byte(s.flashSignature(flashError, q.Get(string(flashError))))) {
		return "", message
	}
	if message := strings.TrimSpace(q.Get(string(flashNotice))); message != "" &&
		hmac.Equal([]byte(sig), []byte(s.flashSignature(flashNotice, q.Get(string(flashNotice))))) {
		return message, ""
	}
	return "", ""
}
