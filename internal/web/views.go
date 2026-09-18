// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"imvault/internal/models"
	"imvault/internal/store"
)

// base is embedded in every page/view struct so the layout always has what it
// needs to render navigation, the CSRF token and flash messages.
type base struct {
	Title       string
	User        *models.User
	CSRFToken   string
	AnonUploads bool
	SignupOpen  bool
	Notice      string
	Error       string
	CurrentPath string
	// SourceURL is the upstream repository, offered in the footer.
	SourceURL string
}

// IsAuthed reports whether a user is signed in. Available to templates.
func (b base) IsAuthed() bool { return b.User != nil }

// IsAdmin reports whether the signed-in user is an administrator.
func (b base) IsAdmin() bool { return b.User != nil && b.User.IsAdmin }

// fileCard is the data for a single tile in a grid.
type fileCard struct {
	File       *models.File
	CSRFToken  string
	Removable  bool
	Selectable bool
	// RemoveURL is the endpoint that detaches or deletes this tile.
	RemoveURL string
}

// fileCardsView backs the reusable grid fragment.
type fileCardsView struct {
	base
	Files      []*models.File
	Removable  bool
	Selectable bool
	// RemovePattern is a printf-style path where %s is the file id.
	RemovePattern string
	Empty         string
}

// paginationView is shared by every paginated listing.
type paginationView struct {
	Page       int
	TotalPages int
	Total      int
	BaseQuery  string
}

// HasPrev / HasNext drive the pager markup.
func (p paginationView) HasPrev() bool { return p.Page > 1 }
func (p paginationView) HasNext() bool { return p.Page < p.TotalPages }

// Pages returns the page numbers to render in the pager.
func (p paginationView) Pages() []int {
	if p.TotalPages <= 1 {
		return nil
	}
	const window = 2
	start, end := p.Page-window, p.Page+window
	if start < 1 {
		start = 1
	}
	if end > p.TotalPages {
		end = p.TotalPages
	}
	out := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, i)
	}
	return out
}

type homeView struct {
	base
	Grid fileCardsView
}

type authView struct {
	base
	Next     string
	Username string
	Email    string
}

type galleryView struct {
	base
	Grid       fileCardsView
	Query      string
	Pagination paginationView
}

type uploadView struct {
	base
	MaxUploadMB     int64
	MaxVideoMB      int64
	MaxVideoSeconds int
	VideoEnabled    bool
	Albums          []*models.Album
}

// apiKeysView backs the API key management page and the panel fragment.
type apiKeysView struct {
	base
	Keys    []*models.APIKey
	NewKey  string
	Error   string
	BaseURL string
}

// adminDashboardView backs the admin overview.
type adminDashboardView struct {
	base
	Stats       store.InstanceStats
	Users       []*models.User
	Grid        fileCardsView
	PendingMail int
	FailedMail  int
	MailEnabled bool
	Notice      string
	Error       string
}

// adminUsersView backs the account list and its controls.
type adminUsersView struct {
	base
	Users      []*models.User
	Pagination paginationView
	Notice     string
	Error      string
	// ResetLink is populated once, immediately after an administrator issues a
	// reset, and never persisted anywhere.
	ResetLink string
	ResetFor  string
	ResetTTL  string
}

// forgotView backs the reset-request form.
type forgotView struct {
	base
	MailEnabled bool
	Submitted   bool
}

// resetView backs the choose-a-new-password form.
type resetView struct {
	base
	Token    string
	Username string
	Error    string
}

// passwordView backs the change-password page and the email verification state.
type passwordView struct {
	base
	User          *models.User
	MailEnabled   bool
	HasEmail      bool
	VerifyPending bool
	Error         string
}

// adminMailView backs the outbound mail queue.
type adminMailView struct {
	base
	Messages    []*models.OutboundMail
	Pending     int
	Failed      int
	MailEnabled bool
	Notice      string
	Error       string
}

// adminFilesView backs instance-wide content moderation.
type adminFilesView struct {
	base
	Grid       fileCardsView
	Pagination paginationView
}

type fileView struct {
	base
	File         *models.File
	Albums       []*models.Album
	TagsFragment tagsFragmentView
	IsOwner      bool
	ShareURL     string
	RawURL       string
}

type tagsView struct {
	base
	Tags []models.Tag
	// ViewerID lets the index mark which tags belong to somebody else.
	ViewerID int64
}

type tagPageView struct {
	base
	Tag        *models.Tag
	Grid       fileCardsView
	Pagination paginationView
}

type albumView struct {
	base
	Album     *models.Album
	Grid      fileCardsView
	IsOwner   bool
	Available fileCardsView
}

type albumsView struct {
	base
	Albums []*models.Album
}

// errorView backs the shared error page.
type errorView struct {
	base
	Code    string
	Message string
}

// tagsFragmentView backs the tag chip list partial.
type tagsFragmentView struct {
	base
	File *models.File
	Tags []models.Tag
	// CanEdit controls whether the remove controls are rendered. A visitor
	// looking at somebody else's public upload must not be offered them.
	CanEdit bool
}

// uploadResultView backs the fragment returned by a completed upload.
type uploadResultView struct {
	base
	Grid      fileCardsView
	Errors    []string
	Anonymous bool
}
