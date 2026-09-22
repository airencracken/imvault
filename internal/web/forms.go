// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"strings"
)

// formRewriteLimit bounds what will be read to look for a semicolon. Anything
// larger is passed through untouched, and anything larger is a file upload,
// which arrives as multipart rather than as a form.
const formRewriteLimit = 1 << 20

// semicolonMW keeps form values that contain a semicolon.
//
// Go's form parser refuses to guess at a bare semicolon — it was once a
// separator alongside "&" and treating it as one was a vulnerability — so it
// reports "invalid semicolon separator in query" and drops the entire setting.
// The parser is right to refuse, but dropping the field is not: a report note
// reading "spam; and there is a lot of it" arrives empty, a search for a
// filename containing one matches nothing, and a password containing one cannot
// be signed in with at all, which is a lockout for somebody who chose it
// legitimately.
//
// The answer is to say what was meant rather than to lose it, so a semicolon is
// percent-encoded before anything parses the request. That is exactly what the
// caller should have sent, and the parser then reads it back as the character
// they typed.
//
// It runs before the CSRF middleware, which parses the form to find its token.
func (s *Server) semicolonMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The query string is part of the URL rather than the body, and a
		// search for a filename containing a semicolon is as ordinary as any
		// other.
		if strings.Contains(r.URL.RawQuery, ";") {
			r.URL.RawQuery = strings.ReplaceAll(r.URL.RawQuery, ";", "%3B")
		}

		if !isFormEncoded(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Read one byte past the limit so an oversized body is recognisable
		// rather than silently truncated.
		body, err := io.ReadAll(io.LimitReader(r.Body, formRewriteLimit+1))
		rest := r.Body
		if err != nil || len(body) > formRewriteLimit {
			// Too large, or unreadable. Hand it on exactly as it arrived: a
			// rewrite would have to be sure it had seen the whole thing.
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), rest))
			next.ServeHTTP(w, r)
			return
		}

		if bytes.ContainsRune(body, ';') {
			body = bytes.ReplaceAll(body, []byte(";"), []byte("%3B"))
			r.ContentLength = int64(len(body))
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		next.ServeHTTP(w, r)
	})
}

// isFormEncoded reports whether this is an ordinary form submission, which is
// the only encoding this rewrites. Multipart carries files and JSON is parsed
// rather than form-decoded; neither has the problem.
func isFormEncoded(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	return mediaType == "application/x-www-form-urlencoded"
}
