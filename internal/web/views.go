// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"imvault/internal/models"
	"imvault/internal/store"
)

// base is embedded in every page/view struct so the layout always has what it
// needs to render navigation, the CSRF token and flash messages.
type base struct {
	Title        string
	SiteName     string
	WelcomeTitle string
	WelcomeText  string
	MascotURL    string
	FaviconURL   string
	User         *models.User
	CSRFToken    string
	AnonUploads  bool
	SignupOpen   bool
	Notice       string
	Error        string
	CurrentPath  string
	// SourceURL is the upstream repository, offered in the footer.
	SourceURL string
	// VisibilityLevels is every level, for the forms that offer the choice.
	VisibilityLevels []models.Visibility
	// DefaultVisibility is what a new upload gets unless the uploader picks
	// something, so the forms can preselect it.
	DefaultVisibility models.Visibility
	// MetadataLevels is every metadata setting, for the controls that offer a
	// choice.
	MetadataLevels []models.MetadataPolicy
	// UseAlpine pulls in Alpine.js. Only the admin pages need it: it carries
	// client-side UI state (a confirmation dialog, table filtering, copy
	// feedback) that htmx is not the right tool for.
	UseAlpine bool
	// FailedMail is only populated on the dashboard, where the mail queue's
	// health is worth surfacing before an administrator goes looking for it.
	FailedMail int
	// OpenReports is how many reports are waiting. It is only counted for
	// accounts that can act on them, so ordinary browsing does not pay for a
	// query nobody will look at.
	OpenReports int
	// OIDCName is the configured identity provider's name, or empty when there
	// is none. Templates offer the button only when it is set.
	OIDCName string
}

// IsAuthed reports whether a user is signed in. Available to templates.
func (b base) IsAuthed() bool { return b.User != nil }

// IsAdmin reports whether the signed-in user is an administrator.
func (b base) IsAdmin() bool { return b.User != nil && b.User.IsAdmin() }

// CanModerate reports whether the signed-in user may remove anybody's content.
func (b base) CanModerate() bool { return b.User != nil && b.User.CanModerate() }

// fileCard is the data for a single tile in a grid.
type fileCard struct {
	File       *models.File
	CSRFToken  string
	Removable  bool
	Selectable bool
	// ShowOwner attributes the upload, which matters where the point is what
	// other people have shared rather than what you have.
	ShowOwner bool
	// RemoveURL is the endpoint that detaches or deletes this tile.
	RemoveURL string
}

// fileCardsView backs the reusable grid fragment.
type fileCardsView struct {
	base
	Files      []*models.File
	Removable  bool
	Selectable bool
	// ShowOwner attributes each tile to the account that uploaded it.
	ShowOwner bool
	// RemovableOwner makes a tile removable when it belongs to that account. It
	// is how a contributor to a shared album may take back their own files
	// without being able to remove anybody else's.
	RemovableOwner *int64
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
	// Invite is the code carried in from a link, so the field arrives filled.
	Invite string
	// InviteOnly asks for a code on an instance that is otherwise open.
	InviteOnly bool
	// RegisterClosed is shown so the page can explain what would admit an
	// account rather than just refusing.
	RegisterClosed bool
}

type galleryView struct {
	base
	Grid       fileCardsView
	Query      string
	Pagination paginationView
}

// recentView is the instance feed: what everybody has shared, newest first.
type recentView struct {
	base
	Grid       fileCardsView
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
	MailEnabled bool
	Blobs       store.BlobStats
	// MaxTotalBytes is the instance ceiling, or zero when there is none, so the
	// dashboard can say "of N" rather than leaving an operator to guess how
	// much room is left.
	MaxTotalBytes int64
	// Notice is reported either inline on a full page load or out of
	// band when htmx swaps a row.
	Notice adminNotice
}

// adminUsersView backs the account list and its controls.
type adminUsersView struct {
	base
	Rows       []adminUserRow
	Pagination paginationView
	// ResetLink is populated once, immediately after an administrator issues a
	// reset, and never persisted anywhere.
	ResetLink string
	ResetFor  string
	ResetTTL  string
	// Notice is reported either inline on a full page load or out of
	// band when htmx swaps a row.
	Notice adminNotice
}

// adminUserRow is one row of the account table, carrying everything its forms
// need so the same partial renders in a page and as an htmx response.
type adminUserRow struct {
	User      *models.User
	CSRFToken string
	// Search is the lowercased text the client-side filter matches against.
	Search string
	// Self marks the signed-in administrator's own row, which offers no
	// disable or delete controls.
	Self bool
	// Error is an action failure to show inline.
	Error string
	// Roles is every role the picker offers.
	Roles []models.Role
}

// adminMailRow is one row of the outbound queue.
type adminMailRow struct {
	Message   *models.OutboundMail
	CSRFToken string
	Search    string
	Error     string
}

// adminNotice is the out-of-band message an admin action reports.
type adminNotice struct {
	Text  string
	Error bool
	// Link, when set, renders a one-time value worth copying. It is shown only
	// in this response and never stored anywhere.
	Link      string
	LinkLabel string
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
	Rows        []adminMailRow
	Pending     int
	Failed      int
	MailEnabled bool
	// Notice is reported either inline on a full page load or out of
	// band when htmx swaps a row.
	Notice adminNotice
}

// adminFilesView backs instance-wide content moderation.
type adminFilesView struct {
	base
	Grid       fileCardsView
	Pagination paginationView
}

type fileView struct {
	base
	Favorited    bool
	File         *models.File
	Albums       []*models.Album
	TagsFragment tagsFragmentView
	// ReportForm is present when the viewer may report this, which is a
	// signed-in member looking at somebody else's upload.
	ReportForm *reportFormView
	// Details is the photograph's own description: when it was taken, with
	// what, and where. It is collapsed on the page and absent when there is
	// nothing to say.
	Details *detailsView
	// IsOwner is the right to change what the file is: its visibility and its
	// tags. A moderator may remove a file but not republish it, so this is
	// narrower than CanDelete.
	IsOwner bool
	// CanDelete is the right to remove the file, which a moderator has for
	// anybody's content.
	CanDelete bool
	ShareURL  string
	RawURL    string
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
	Album *models.Album
	Grid  fileCardsView
	// ReportForm is present when the viewer may report this album.
	ReportForm *reportFormView
	// IsOwner is the right to change or delete the album itself.
	IsOwner bool
	// CanContribute is the right to add one's own files, which a shared album
	// grants to any account that can see it.
	CanContribute bool
	Available     fileCardsView
	Levels        []models.Visibility
	Access        []models.AlbumAccess
}

type albumsView struct {
	base
	// Albums is the viewer's own.
	Albums []*models.Album
	// Shared is everybody else's that the viewer can see, which is how a
	// shared album is discovered.
	Shared []*models.Album
	Levels []models.Visibility
	Access []models.AlbumAccess
}

// reportFormView backs the report control on a file or album page.
type reportFormView struct {
	base
	Kind       string
	TargetKind models.TargetKind
	TargetID   string
	Reasons    []models.ReportReason
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
