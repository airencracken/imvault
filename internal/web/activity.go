// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"github.com/airencracken/comfylib/analytics"
	"net/http"
)

func (s *Server) trackFile(next http.HandlerFunc) http.HandlerFunc {
	return analytics.Track(s.store.AnalyticsDB(), next, func(r *http.Request) []analytics.Event {
		u := currentUser(r.Context())
		if u == nil {
			return nil
		}
		file, err := s.store.FileByID(r.Context(), r.PathValue("id"))
		if err != nil {
			return nil
		}
		kind := analytics.Image
		if file.IsVideo() {
			kind = analytics.Video
		}
		return []analytics.Event{{Kind: kind, Member: analytics.MemberID(u.ID), Item: file.ID}}
	})
}
