// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"net/http"
	"time"

	"imvault/internal/models"
	"imvault/internal/store"
)

// notFound renders the shared 404 page.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request, message string) {
	if message == "" {
		message = "That page could not be found."
	}
	s.renderPage(w, http.StatusNotFound, "notfound", errorView{
		base:    s.base(r, "Not found"),
		Code:    "404",
		Message: message,
	})
}

// handleGallery lists the signed-in user's own uploads.
func (s *Server) handleGallery(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	query := r.URL.Query().Get("q")

	filter := store.FileQuery{
		OwnerID: &user.ID,
		Search:  query,
	}

	total, err := s.store.CountFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("gallery: count", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	page, pg := pagination(r, total)
	filter.Limit = defaultPageSize
	filter.Offset = (page - 1) * defaultPageSize

	files, err := s.store.ListFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("gallery: list", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	s.renderPage(w, http.StatusOK, "gallery", galleryView{
		base:       s.base(r, "Your gallery"),
		Grid:       s.grid(r, files, true, false, "", "You have not uploaded anything yet."),
		Query:      query,
		Pagination: pg,
	})
}

// handleFilePage shows a single image with its metadata and controls.
func (s *Server) handleFilePage(w http.ResponseWriter, r *http.Request) {
	file, ok := s.lookupVisibleFile(w, r)
	if !ok {
		return
	}

	view := fileView{
		base:     s.base(r, file.OriginalName),
		File:     file,
		IsOwner:  canEditFile(currentUser(r.Context()), file),
		ShareURL: s.absoluteURL(r, "/f/"+file.ID),
		RawURL:   s.absoluteURL(r, "/f/"+file.ID+"/raw"),
		TagsFragment: tagsFragmentView{
			base:    s.base(r, file.OriginalName),
			File:    file,
			Tags:    file.Tags,
			CanEdit: canEditFile(currentUser(r.Context()), file),
		},
	}

	if view.IsOwner {
		albums, err := s.store.AlbumsForFile(r.Context(), file.ID)
		if err != nil {
			s.log.Error("file page: albums for file", "error", err)
		}
		view.Albums = albums
	}

	s.renderPage(w, http.StatusOK, "file", view)
}

// handleShortLink redirects the short share URL to the canonical page.
func (s *Server) handleShortLink(w http.ResponseWriter, r *http.Request) {
	file, ok := s.lookupVisibleFile(w, r)
	if !ok {
		return
	}
	http.Redirect(w, r, "/f/"+file.ID, http.StatusSeeOther)
}

// handleTagsPage lists every tag in use that the viewer can actually see.
func (s *Server) handleTagsPage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	tags, err := s.store.ListTags(r.Context(), userIDPtr(user), 200)
	if err != nil {
		s.log.Error("tags page: list", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	view := tagsView{
		base: s.base(r, "Tags"),
		Tags: tags,
	}
	if user != nil {
		view.ViewerID = user.ID
	}

	s.renderPage(w, http.StatusOK, "tags", view)
}

// handleTagPage lists the files carrying one account's tag.
//
// Tags are addressed as /tags/{username}/{slug}, because the slug alone is only
// unique within an account.
func (s *Server) handleTagPage(w http.ResponseWriter, r *http.Request) {
	viewer := userIDPtr(currentUser(r.Context()))

	owner, err := s.store.UserByUsername(r.Context(), r.PathValue("username"))
	if err != nil {
		s.notFound(w, r, "No tag by that name.")
		return
	}

	tag, err := s.store.TagBySlugInUser(r.Context(), owner.ID, r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r, "No tag by that name.")
			return
		}
		s.log.Error("tag page: lookup", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	// The tag may exist but label nothing this viewer can see; that must look
	// exactly like a tag that does not exist.
	visible, err := s.store.CountTagVisibleTo(r.Context(), tag.ID, viewer)
	if err != nil {
		s.log.Error("tag page: count", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	if visible == 0 {
		s.notFound(w, r, "No tag by that name.")
		return
	}
	tag.Count = visible

	tagID := tag.ID
	filter := store.FileQuery{TagID: &tagID}
	if viewer != nil {
		filter.VisibleTo = viewer
	} else {
		filter.PublicOnly = true
	}

	total, err := s.store.CountFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("tag page: count files", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	page, pg := pagination(r, total)
	filter.Limit = defaultPageSize
	filter.Offset = (page - 1) * defaultPageSize

	files, err := s.store.ListFiles(r.Context(), filter)
	if err != nil {
		s.log.Error("tag page: list", "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	s.renderPage(w, http.StatusOK, "tag", tagPageView{
		base:       s.base(r, "#"+tag.Name),
		Tag:        tag,
		Grid:       s.grid(r, files, false, false, "", "No images carry this tag yet."),
		Pagination: pg,
	})
}

// lookupVisibleFile resolves the {id} path value, enforcing visibility and
// retention. It writes the error response itself and reports whether the
// caller should continue.
func (s *Server) lookupVisibleFile(w http.ResponseWriter, r *http.Request) (file *models.File, ok bool) {
	id := r.PathValue("id")

	f, err := s.store.FileByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r, "That image does not exist.")
			return nil, false
		}
		s.log.Error("lookup file", "id", id, "error", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return nil, false
	}

	// An expired upload is gone even if the reaper has not run yet.
	if f.Expired(time.Now()) {
		s.renderPage(w, http.StatusGone, "notfound", errorView{
			base:    s.base(r, "Expired"),
			Code:    "410",
			Message: "This upload has expired and been removed.",
		})
		return nil, false
	}

	if !canViewFile(currentUser(r.Context()), f) {
		// Do not reveal that a private file exists.
		s.notFound(w, r, "That image does not exist.")
		return nil, false
	}

	return f, true
}
