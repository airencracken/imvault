// SPDX-License-Identifier: AGPL-3.0-or-later
package metadata

import "time"

// CaptureDate converts the existing camera-date display value to a sortable
// wall time. No timezone is inferred for cameras that did not record one.
// Missing or malformed dates remain unknown rather than becoming upload dates.
func CaptureDate(raw string) string {
	details := DecodeDetails(raw)
	if details == nil {
		return ""
	}
	when, err := time.Parse("2 January 2006 at 15:04", details.Taken)
	if err != nil || when.Year() < 1 || when.Year() > 9999 {
		return ""
	}
	return when.Format("2006-01-02T15:04:05")
}
