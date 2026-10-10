// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/airencracken/comfylib/memberprofile"
	"imvault/internal/store"
)

type memberProfileView struct {
	base
	Member         store.MemberProfile
	Biography      memberprofile.Profile
	AnimateAvatars bool
}

func (s *Server) handleMemberProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	p, err := s.store.MemberProfile(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.log.Error("load member profile", "error", err)
		http.Error(w, "Could not load the profile.", 500)
		return
	}
	view := memberProfileView{base: s.base(r, p.Username+"'s profile"), Member: p}
	view.Notice, view.Error = s.flash(r)
	s.renderPage(w, 200, "member_profile", view)
}

func (s *Server) handleProfileSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, err := s.store.MemberProfile(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		s.log.Error("load own profile", "error", err)
		http.Error(w, "Could not load your profile.", 500)
		return
	}
	view := memberProfileView{base: s.base(r, "Your profile"), Biography: p.Biography, Member: p}
	view.AnimateAvatars, err = s.store.AnimateAvatars(r.Context(), p.ID)
	if err != nil {
		s.avatarError(w, err)
		return
	}
	view.Notice, view.Error = s.flash(r)
	s.renderPage(w, 200, "profile_settings", view)
}

func (s *Server) handleProfileSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, err := memberprofile.ParseForm(r.PostForm)
	if err != nil {
		member, loadErr := s.store.MemberProfile(r.Context(), currentUser(r.Context()).ID)
		if loadErr != nil {
			s.avatarError(w, loadErr)
			return
		}
		animate, loadErr := s.store.AnimateAvatars(r.Context(), member.ID)
		if loadErr != nil {
			s.avatarError(w, loadErr)
			return
		}
		view := memberProfileView{base: s.base(r, "Your profile"), Biography: p, Member: member, AnimateAvatars: animate}
		view.Error = err.Error()
		s.renderPage(w, 422, "profile_settings", view)
		return
	}
	if err = s.store.SetMemberProfile(r.Context(), currentUser(r.Context()).ID, p); err != nil {
		s.log.Error("save profile", "error", err)
		http.Error(w, "Could not save your profile.", 500)
		return
	}
	s.redirectFlash(w, r, "/settings/profile", flashNotice, "Profile saved.")
}
