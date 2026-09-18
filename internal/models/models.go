// SPDX-License-Identifier: AGPL-3.0-or-later

// Package models contains the plain data types shared between the store and
// the HTTP layer.
package models

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// User is a registered account.
type User struct {
	ID           int64
	Username     string
	Email        string
	PasswordHash string
	IsAdmin      bool
	Disabled     bool
	// EmailVerified records that the address was confirmed, when the instance
	// has mail configured. It never gates access: an unverified account works
	// exactly like a verified one.
	EmailVerified bool
	CreatedAt     time.Time

	// QuotaBytes caps how much this account may store. Zero means unlimited.
	QuotaBytes int64
	// StorageUsed is the running total of original bytes owned by the account.
	StorageUsed int64

	// TOTPEnabled reports whether a second factor is in force. TOTPSecret holds
	// the shared secret, encrypted: unlike a session or an API key it has to be
	// readable again to check a code, so it cannot be a digest.
	TOTPEnabled bool
	TOTPSecret  string
	// TOTPLastStep is the last time step accepted, so a code cannot be
	// replayed for the rest of its window.
	TOTPLastStep uint64
}

// TwoFactorRequired reports whether signing in needs a second factor.
func (u *User) TwoFactorRequired() bool { return u.TOTPEnabled }

// Unlimited reports whether the account has no storage cap.
func (u *User) Unlimited() bool { return u.QuotaBytes <= 0 }

// RemainingBytes is how much more the account may store, or 0 when unlimited.
func (u *User) RemainingBytes() int64 {
	if u.Unlimited() {
		return 0
	}
	if remaining := u.QuotaBytes - u.StorageUsed; remaining > 0 {
		return remaining
	}
	return 0
}

// UsagePercent is how full the account's quota is, clamped to 0..100. It
// reports 0 for unlimited accounts, which have no meaningful percentage.
func (u *User) UsagePercent() int {
	if u.Unlimited() {
		return 0
	}
	if u.StorageUsed >= u.QuotaBytes {
		return 100
	}
	return int(u.StorageUsed * 100 / u.QuotaBytes)
}

// OverQuota reports whether the account has already exceeded its cap, which can
// happen if a quota is lowered after the fact.
func (u *User) OverQuota() bool {
	return !u.Unlimited() && u.StorageUsed > u.QuotaBytes
}

// UsageLabel renders the account's usage for display.
func (u *User) UsageLabel() string {
	if u.Unlimited() {
		return HumanSize(u.StorageUsed) + " of unlimited"
	}
	return HumanSize(u.StorageUsed) + " of " + HumanSize(u.QuotaBytes)
}

// Kind classifies what an upload actually is.
type Kind string

const (
	// KindImage is a still image.
	KindImage Kind = "image"
	// KindAnimated is a GIF or animated WebP, preserved as-is and played by
	// the browser. Its thumbnail is a still first frame.
	KindAnimated Kind = "animated"
	// KindVideo is a short clip container (WebM/MP4/MOV), stored untouched.
	KindVideo Kind = "video"
)

// ParseKind converts a stored string into a Kind, defaulting to KindImage.
func ParseKind(s string) Kind {
	switch Kind(s) {
	case KindAnimated:
		return KindAnimated
	case KindVideo:
		return KindVideo
	default:
		return KindImage
	}
}

// File is a stored image, animation or clip.
//
// UserID is nil for anonymous uploads; those carry an ExpiresAt deadline.
type File struct {
	ID           string
	UserID       *int64
	OriginalName string
	Ext          string
	Mime         string
	Size         int64
	Width        int
	Height       int
	SHA256       string
	ObjectKey    string
	ThumbKey     string
	PreviewKey   string
	IsPublic     bool
	Kind         Kind
	DurationMS   int64
	FrameCount   int
	Views        int64
	CreatedAt    time.Time
	ExpiresAt    *time.Time

	// Populated by list queries that join for display.
	Username string
	Tags     []Tag
}

// Expired reports whether the file has passed its retention deadline.
func (f *File) Expired(at time.Time) bool {
	return f.ExpiresAt != nil && !f.ExpiresAt.After(at)
}

// Anonymous reports whether the file was uploaded without an account.
func (f *File) Anonymous() bool { return f.UserID == nil }

// IsVideo reports whether the file is a video clip.
func (f *File) IsVideo() bool { return f.Kind == KindVideo }

// IsAnimated reports whether the file is an animated image.
func (f *File) IsAnimated() bool { return f.Kind == KindAnimated }

// HasMotion reports whether the file moves, either as a clip or an animation.
func (f *File) HasMotion() bool { return f.IsVideo() || f.IsAnimated() }

// IsAnimatedGIF is used by templates to decide whether a grid thumbnail can be
// swapped for the moving version on hover.
func (f *File) IsAnimatedGIF() bool { return f.IsAnimated() && f.Ext == "gif" }

// PreviewKeyOrObject returns the preview rendition when one exists, otherwise
// the original. Animations and clips have no separate preview: the original is
// the thing the browser should play.
func (f *File) PreviewKeyOrObject() string {
	if f.PreviewKey != "" {
		return f.PreviewKey
	}
	return f.ObjectKey
}

// PreviewMime is the content type for the preview endpoint.
func (f *File) PreviewMime() string {
	if f.PreviewKey == "" {
		return f.Mime
	}
	return ""
}

// DurationLabel renders a clip length as "m:ss".
func (f *File) DurationLabel() string {
	if f.DurationMS <= 0 {
		return ""
	}
	total := f.DurationMS / 1000
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

// HumanSize renders the file size for display.
func (f *File) HumanSize() string { return HumanSize(f.Size) }

// Dimensions renders "1920×1080", or "" when unknown.
func (f *File) Dimensions() string {
	if f.Width == 0 || f.Height == 0 {
		return ""
	}
	return fmt.Sprintf("%d×%d", f.Width, f.Height)
}

// Album is a user-owned collection of files.
type Album struct {
	ID          int64
	UserID      int64
	Title       string
	Slug        string
	Description string
	IsPublic    bool
	CreatedAt   time.Time

	// Populated by list queries.
	Username  string
	FileCount int
}

// Tag is a label applied to files. Tags belong to an account: two people can
// both use the name "beach" without sharing a row, a count or a lifetime.
type Tag struct {
	ID     int64
	UserID int64
	Name   string
	Slug   string
	Count  int

	// Username is the owning account's name, populated by list queries so the
	// UI can build owner-qualified tag URLs.
	Username string
}

// OwnerSlug joins the owner's username and the tag slug, which together
// identify a tag unambiguously across the instance.
func (t Tag) OwnerSlug() string { return t.Username + "/" + t.Slug }

// OutboundMail is a queued email message.
//
// Messages are persisted before delivery is attempted, so a relay outage delays
// them rather than losing them. SentAt and FailedAt are mutually exclusive:
// FailedAt is set once the message has exhausted its retries and needs a
// decision from an operator.
type OutboundMail struct {
	ID            int64
	Recipient     string
	Subject       string
	Body          string
	CreatedAt     time.Time
	Attempts      int
	NextAttemptAt time.Time
	SentAt        *time.Time
	FailedAt      *time.Time
	LastError     string
}

// MailStatus describes where a message has got to.
func (m *OutboundMail) MailStatus() string {
	switch {
	case m.SentAt != nil:
		return "sent"
	case m.FailedAt != nil:
		return "failed"
	default:
		return "queued"
	}
}

// Pending reports whether the message is still awaiting delivery.
func (m *OutboundMail) Pending() bool { return m.SentAt == nil && m.FailedAt == nil }

// APIKey is a bearer token that authenticates the programmatic API.
// KeyHash is never rendered; it exists only so the auth middleware can compare
// a presented key against the stored digest.
type APIKey struct {
	ID         int64
	UserID     int64
	Name       string
	Prefix     string
	KeyHash    string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time

	// Populated by list queries.
	Username string
}

// Expired reports whether the key has passed its expiry.
func (k *APIKey) Expired(at time.Time) bool {
	return k.ExpiresAt != nil && !k.ExpiresAt.After(at)
}

// Masked renders the key as it can safely be shown after creation.
func (k *APIKey) Masked() string { return "imv_" + k.Prefix + "_" + strings.Repeat("•", 12) }

// HumanLastUsed describes when the key was last presented.
func (k *APIKey) HumanLastUsed() string {
	if k.LastUsedAt == nil {
		return "never"
	}
	return HumanTime(*k.LastUsedAt)
}

// HumanExpiry describes when the key lapses.
func (k *APIKey) HumanExpiry() string {
	if k.ExpiresAt == nil {
		return "never"
	}
	if k.Expired(time.Now()) {
		return "expired"
	}
	return (*k.ExpiresAt).Format("2 Jan 2006")
}

// ExpiryLabel describes how long the file has left, or "" when it never expires.
func (f *File) ExpiryLabel() string {
	if f.ExpiresAt == nil {
		return ""
	}
	d := time.Until(*f.ExpiresAt)
	switch {
	case d <= 0:
		return "expired"
	case d < time.Hour:
		return fmt.Sprintf("expires in %dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("expires in %dh", int(d.Hours()))
	default:
		return fmt.Sprintf("expires in %dd", int(d.Hours()/24))
	}
}

// Truncate shortens s to at most max runes, never splitting a UTF-8 sequence.
func Truncate(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i]
		}
		count++
	}
	return s
}

// HumanSize renders a byte count in binary units.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// HumanTime renders a timestamp as a coarse relative age.
func HumanTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
