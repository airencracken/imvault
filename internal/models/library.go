// SPDX-License-Identifier: AGPL-3.0-or-later
package models

import "time"

const TrashRetention = 30 * 24 * time.Hour

func (f *File) OwnedBy(userID int64) bool { return f.UserID != nil && *f.UserID == userID }

func (f *File) CaptureLabel() string {
	when, err := time.Parse("2006-01-02T15:04:05", f.TakenAt)
	if err != nil {
		return "Unknown date taken"
	}
	return "Taken " + when.Format("2 Jan 2006, 15:04")
}

func (f *File) TrashDeadline() time.Time {
	if f.TrashedAt == nil {
		return time.Time{}
	}
	return f.TrashedAt.Add(TrashRetention)
}

func (f *File) Restorable() bool {
	now := time.Now()
	return f.TrashedAt != nil && f.TrashDeadline().After(now) && !f.Expired(now)
}
