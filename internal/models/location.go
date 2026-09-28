// SPDX-License-Identifier: AGPL-3.0-or-later

package models

// LocationLabel differs from the EXIF label because location inherits the EXIF
// choice, including its default based on file visibility.
func (p MetadataPolicy) LocationLabel() string {
	if p == MetadataInherit {
		return "Follow EXIF"
	}
	return p.Label()
}

func (p MetadataPolicy) LocationExplain() string {
	switch p {
	case MetadataShown:
		return "Share GPS coordinates with everyone who can view the file"
	case MetadataHidden:
		return "Hide GPS coordinates from other viewers and downloads"
	default:
		return "Use the EXIF sharing setting for GPS coordinates too"
	}
}

// WithFallback resolves an inherited location choice. The fallback is the
// file's resolved EXIF policy or an album's explicit EXIF policy.
func (p MetadataPolicy) WithFallback(fallback MetadataPolicy) MetadataPolicy {
	if p == MetadataInherit || !p.Valid() {
		return fallback
	}
	return p
}
