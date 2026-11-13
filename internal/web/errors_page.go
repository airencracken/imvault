// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/airencracken/comfylib/problem"
)

// problems writes Comfylib's plain-language error responses inside Imvault's
// layout, or JSON for API clients.
func (s *Server) problems() problem.Writer {
	return problem.Writer{
		Logger: s.log,
		Layout: func(w http.ResponseWriter, r *http.Request, page problem.Page) error {
			var buf strings.Builder
			view := errorView{base: s.base(r, page.Title), Code: strconv.Itoa(page.Status), Message: page.Message}
			if err := s.render.page(&buf, "notfound", view, preferredTimezone(r)); err != nil {
				return err
			}
			_, err := w.Write([]byte(buf.String()))
			return err
		},
	}
}

// fail renders the styled error page. The message is one plain sentence for
// the person who hit the problem; anything an operator needs belongs in the
// log, written before this is called. An empty message uses Comfylib's
// default for the status.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.problems().Write(w, r, status, message, nil)
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
