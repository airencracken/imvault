// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// fail renders the styled error page for a browser request. The message is one
// plain sentence for the person who hit the problem; anything an operator
// needs belongs in the log, written before this is called.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	if message == "" {
		message = http.StatusText(status) + "."
	}
	s.renderPage(w, r, status, "notfound", errorView{
		base:    s.base(r, errorTitle(status)),
		Code:    strconv.Itoa(status),
		Message: message,
	})
}

func errorTitle(status int) string {
	switch {
	case status == http.StatusNotFound:
		return "Not found"
	case status == http.StatusForbidden:
		return "Not allowed"
	case status == http.StatusTooManyRequests:
		return "Slow down"
	case status >= 500:
		return "Something went wrong"
	}
	return "Couldn't do that"
}

// sentence turns a lower-case validation message, shared with the JSON API,
// into a sentence for the error page.
func sentence(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(message)
	message = string(unicode.ToUpper(first)) + message[size:]
	if !strings.HasSuffix(message, ".") {
		message += "."
	}
	return message
}
