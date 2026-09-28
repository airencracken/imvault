// SPDX-License-Identifier: AGPL-3.0-or-later

package models

import "testing"

func TestLocationFallbackAndLabels(t *testing.T) {
	if MetadataInherit.LocationLabel() != "Follow EXIF" {
		t.Fatal("inherit label does not explain the fallback")
	}
	for _, fallback := range MetadataLevels() {
		for _, policy := range append(MetadataLevels(), MetadataPolicy(""), MetadataPolicy("invalid")) {
			got := policy.WithFallback(fallback)
			want := policy
			if policy == MetadataInherit || !policy.Valid() {
				want = fallback
			}
			if got != want {
				t.Fatalf("%s with %s = %s", policy, fallback, got)
			}
			if policy.LocationExplain() == "" {
				t.Fatal("missing explanation")
			}
		}
	}
}
