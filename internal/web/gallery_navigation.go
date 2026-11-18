// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"imvault/internal/models"
	"imvault/internal/store"
)

type galleryGroup struct {
	Title string
	Grid  fileCardsView
}

func groupGallery(grid fileCardsView, dates libraryDates) []galleryGroup {
	if dates.Sort != "taken" {
		return nil
	}
	var groups []galleryGroup
	for _, file := range grid.Files {
		title := "Unknown date taken"
		if taken, err := time.Parse("2006-01-02T15:04:05", file.TakenAt); err == nil {
			title = taken.Format("January 2006")
		}
		if len(groups) == 0 || groups[len(groups)-1].Title != title {
			group := galleryGroup{Title: title, Grid: grid}
			group.Grid.Files = nil
			group.Grid.ShowTaken = false
			groups = append(groups, group)
		}
		group := &groups[len(groups)-1]
		group.Grid.Files = append(group.Grid.Files, file)
	}
	return groups
}

type galleryNavigation struct {
	Back     string
	Previous string
	Next     string
}

func galleryQuery(search string, dates libraryDates, page int, detail bool) url.Values {
	query := url.Values{}
	for key, value := range map[string]string{"q": search, "sort": dates.Sort, "year": dates.Year, "month": dates.Month} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if page > 1 {
		query.Set("page", strconv.Itoa(page))
	}
	if detail {
		query.Set("from", "gallery")
	}
	return query
}

func (s *Server) galleryNavigation(r *http.Request, user *models.User, file *models.File) galleryNavigation {
	query := r.URL.Query()
	if query.Get("from") != "gallery" || user == nil || file.UserID == nil || *file.UserID != user.ID {
		return galleryNavigation{}
	}
	page := queryInt(r, "page", 1)
	if page < 1 || page > math.MaxInt/defaultPageSize {
		return galleryNavigation{}
	}
	filter := store.FileQuery{OwnerID: &user.ID, Search: query.Get("q")}
	dates, err := applyLibraryDates(query, &filter)
	if err != nil {
		return galleryNavigation{}
	}
	start := (page - 1) * defaultPageSize
	filter.Offset = max(0, start-1)
	filter.Limit = defaultPageSize + 2
	files, err := s.store.ListFiles(r.Context(), filter)
	if err != nil {
		s.log.Warn("gallery: file navigation", "error", err)
		return galleryNavigation{}
	}

	back := galleryQuery(filter.Search, dates, page, false).Encode()
	nav := galleryNavigation{Back: "/gallery?" + back + "#file-" + file.ID}
	for index, match := range files {
		if match.ID == file.ID {
			nav.Previous = galleryNeighbor(files, index-1, filter, dates)
			nav.Next = galleryNeighbor(files, index+1, filter, dates)
			break
		}
	}
	return nav
}

func galleryNeighbor(files []*models.File, index int, filter store.FileQuery, dates libraryDates) string {
	if index < 0 || index >= len(files) {
		return ""
	}
	page := (filter.Offset+index)/defaultPageSize + 1
	return "/f/" + files[index].ID + "?" + galleryQuery(filter.Search, dates, page, true).Encode()
}
